package election

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository 提供 Leader Election 的 DB 存取操作
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 建立一個新的 Election Repository
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// leaseDuration 定義租約的有效時間
const leaseDuration = 15 * time.Second

// AcquireLock 嘗試競選 Leader，回傳 (fencingToken, acquired, error)
// 使用 PostgreSQL 的 upsert + WHERE 條件原子操作，確保只有一個實例能獲得新租約：
//   - 若沒有任何人持有鎖（鎖不存在）→ 成功插入，成為 Leader
//   - 若舊租約已過期                 → 成功更新，取代舊 Leader，成為新 Leader
//   - 若有人持有有效租約             → 不做任何事，回傳 false
func (r *Repository) AcquireLock(ctx context.Context, partition, instanceID string) (fencingToken int64, acquired bool, err error) {
	now := time.Now().UnixMilli()
	expiresAt := time.Now().Add(leaseDuration).UnixMilli()

	row := r.pool.QueryRow(ctx, `
		INSERT INTO partition_leader_locks (partition, leader_id, fencing_token, expires_at)
		VALUES ($1, $2, 1, $3)
		ON CONFLICT (partition) DO UPDATE
		  SET leader_id     = EXCLUDED.leader_id,
		      fencing_token = partition_leader_locks.fencing_token + 1,
		      expires_at    = EXCLUDED.expires_at
		  WHERE partition_leader_locks.expires_at < $4
		RETURNING fencing_token`,
		partition, instanceID, expiresAt, now,
	)

	err = row.Scan(&fencingToken)
	if err != nil {
		// pgx 找不到任何行（不符合 WHERE 條件）時回傳 pgx.ErrNoRows
		// 這代表目前有人持有有效租約，本次競選失敗
		return 0, false, nil
	}
	return fencingToken, true, nil
}

// ExtendLease 由目前的 Leader 呼叫，延長自己的租約（更新 expires_at）
// 只有目前 Leader ID 匹配、且 fencing_token 相符才能成功延長
func (r *Repository) ExtendLease(ctx context.Context, partition, instanceID string, fencingToken int64) error {
	expiresAt := time.Now().Add(leaseDuration).UnixMilli()
	cmdTag, err := r.pool.Exec(ctx, `
		UPDATE partition_leader_locks
		SET expires_at = $1
		WHERE partition = $2 AND leader_id = $3 AND fencing_token = $4`,
		expiresAt, partition, instanceID, fencingToken,
	)
	if err != nil {
		return fmt.Errorf("延長租約失敗: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		// 0 表示我們的 FencingToken 已被新主取代，發生了腦裂情況
		return fmt.Errorf("租約已失效（可能已發生選主切換），FencingToken: %d", fencingToken)
	}
	return nil
}

// ReleaseLock 由 Leader 在優雅關機前主動釋放租約
// 加速 Standby 的接管時間，避免等待租約自然過期
func (r *Repository) ReleaseLock(ctx context.Context, partition, instanceID string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM partition_leader_locks
		WHERE partition = $1 AND leader_id = $2`,
		partition, instanceID,
	)
	return err
}

// ValidateFencingToken 驗證訊息中的 FencingToken 是否仍與目前 DB 中的 Leader 相符。
// 用於結算服務攔截殭屍訊息（舊 Leader 在失去身份後仍發出的殘留訊息）。
// 傳入 token <= 0 時直接通過驗證（向後相容舊版不帶 Token 的訊息）。
// 傳入 token < currentToken 時代表此為殭屍訊息，回傳 (false, nil)。
func (r *Repository) ValidateFencingToken(ctx context.Context, partition string, token int64) (valid bool, err error) {
	if token <= 0 {
		// token 為 0 代表訊息來自尚未整合 FencingToken 的舊版，允許通過以保持向後相容
		return true, nil
	}

	var currentToken int64
	err = r.pool.QueryRow(ctx, `
		SELECT fencing_token FROM partition_leader_locks
		WHERE partition = $1`,
		partition,
	).Scan(&currentToken)
	if err != nil {
		// 若 DB 中找不到鎖記錄（ErrNoRows），代表目前沒有 Leader 持有鎖
		// 允許通過：結算服務做後續冪等保護即可
		return true, nil
	}

	// token < currentToken：此訊息來自舊一代 Leader，是殭屍訊息，應拒絕
	if token < currentToken {
		return false, nil
	}
	return true, nil
}

