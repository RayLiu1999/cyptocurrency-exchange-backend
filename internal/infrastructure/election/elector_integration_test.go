//go:build integration

package election

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupElectionIntegration 配置整合測試用的資料庫連線
func setupElectionIntegration(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()

	// 嘗試從環境變數取得 DB 連線，若無則使用本機預設值
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:123qwe@localhost:5432/exchange?sslmode=disable"
	}

	// 建立 PostgreSQL 連線池
	pool, err := db.NewPostgresPool(context.Background(), db.DefaultDBConfig(dbURL))
	if err != nil {
		t.Skipf("skip: 無法建立連線池 (%v)", err) // 若環境不支援則跳過測試
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("skip: 資料庫未啟動 (%v)", err)
	}

	// 初始化資料表架構
	ensureElectionSchema(t, pool)
	// 清空鎖表格，確保測試案例之間是獨立且乾淨的
	_, err = pool.Exec(context.Background(), `TRUNCATE partition_leader_locks`)
	require.NoError(t, err)

	// 測試結束後自動關閉連線
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `TRUNCATE partition_leader_locks`)
		pool.Close()
	})

	return NewRepository(pool), pool
}

// ensureElectionSchema 確保資料表結構存在（若沒跑 migration 時的保險）
func ensureElectionSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS partition_leader_locks (
			partition VARCHAR(128) NOT NULL PRIMARY KEY,
			leader_id VARCHAR(255) NOT NULL,
			fencing_token BIGINT NOT NULL DEFAULT 1,
			expires_at BIGINT NOT NULL
		)`)
	require.NoError(t, err)
}

// TestElectorRun_Integration_FailoverAfterGracefulShutdown 驗證：完整故障轉移流程
// 測試兩台 Elector 如何在其中一台正常關機時，由另一台接手並確保 Token 單調遞增。
func TestElectorRun_Integration_FailoverAfterGracefulShutdown(t *testing.T) {
	repo, _ := setupElectionIntegration(t)

	// 建立兩台模擬實例 A 與 B，並將心跳頻率設得極短（20ms）加速測試
	electorA := NewElector(repo, "itest-election-failover", "instance-a")
	electorB := NewElector(repo, "itest-election-failover", "instance-b")
	electorA.renewInterval = 20 * time.Millisecond
	electorB.renewInterval = 20 * time.Millisecond

	aLeader := make(chan int64, 1)
	bLeader := make(chan int64, 1)
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	doneA := make(chan struct{})
	doneB := make(chan struct{})

	// 啟動 A 與 B 同時競爭
	go func() {
		defer close(doneA)
		electorA.Run(ctxA, func() {
			select {
			case aLeader <- electorA.FencingToken():
			default:
			}
		}, nil)
	}()
	go func() {
		defer close(doneB)
		electorB.Run(ctxB, func() {
			select {
			case bLeader <- electorB.FencingToken():
			default:
			}
		}, nil)
	}()

	// 1. 等待第一位勝出者（可能是 A 或 B）
	var firstToken int64
	var secondLeader <-chan int64
	select {
	case firstToken = <-aLeader:
		// A 先當選，我們取消 A 模擬關機，觀察 B 是否接手
		cancelA()
		secondLeader = bLeader
	case firstToken = <-bLeader:
		// B 先當選，我們取消 B 模擬關機，觀察 A 是否接手
		cancelB()
		secondLeader = aLeader
	case <-time.After(2 * time.Second):
		t.Fatal("逾時：這一段時間內沒有任何實例成為 Leader")
	}

	// 2. 等待第二位勝出者接管
	var secondToken int64
	select {
	case secondToken = <-secondLeader:
	case <-time.After(2 * time.Second):
		t.Fatal("逾時：第二台備機並未在時間內自動接管")
	}

	// 3. 核心檢查點：第二任 Token 必須大於第一任，確保順序正確
	assert.Greater(t, secondToken, firstToken)

	// 清理結束測試
	cancelA()
	cancelB()
	<-doneA
	<-doneB
}

// TestRepositoryValidateFencingToken_Integration_RejectsStaleToken 驗證：殭屍訊息攔截邏輯
func TestRepositoryValidateFencingToken_Integration_RejectsStaleToken(t *testing.T) {
	repo, pool := setupElectionIntegration(t)
	ctx := context.Background()

	// 1. A 取得第一個 Token
	token1, acquired, err := repo.AcquireLock(ctx, "itest-election-token", "instance-a")
	require.NoError(t, err)
	require.True(t, acquired)

	// 2. 直接操作 DB 強迫租約過期（模擬網路超時造成的腦裂臨界點）
	_, err = pool.Exec(ctx, `
		UPDATE partition_leader_locks
		SET expires_at = $1
		WHERE partition = $2`,
		time.Now().Add(-time.Second).UnixMilli(), "itest-election-token",
	)
	require.NoError(t, err)

	// 3. B 此時應能爭奪成功，拿到更大 (遞增) 的 Token
	token2, acquired, err := repo.AcquireLock(ctx, "itest-election-token", "instance-b")
	require.NoError(t, err)
	require.True(t, acquired)
	assert.Greater(t, token2, token1)

	// 4. 進行攔截驗證：結算系統應拒絕舊 Token 的訊息，接受新 Token 的訊息
	valid, err := repo.ValidateFencingToken(ctx, "itest-election-token", token1)
	require.NoError(t, err)
	assert.False(t, valid) // 應判定為過期（Invalid/Stale）

	valid, err = repo.ValidateFencingToken(ctx, "itest-election-token", token2)
	require.NoError(t, err)
	assert.True(t, valid) // 現任應判定為有效
}
