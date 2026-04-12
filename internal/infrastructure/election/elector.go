package election

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"go.uber.org/zap"
)

type electorRepository interface {
	AcquireLock(ctx context.Context, partition, instanceID string) (fencingToken int64, acquired bool, err error)
	ExtendLease(ctx context.Context, partition, instanceID string, fencingToken int64) error
	ReleaseLock(ctx context.Context, partition, instanceID string) error
}

var _ electorRepository = (*Repository)(nil)

// Elector 實作 Leader Election 邏輯，帶有租約自動延長機制
// 設計保證：
//   - 任意時刻至多一個實例是 Leader（由 DB Upsert WHERE 保證）
//   - Fencing Token 單調遞增，防止腦裂（舊 Leader 復活後寫入會被 DB 拒絕）
//   - 優雅關機時主動釋放租約，加速 Standby 接管
type Elector struct {
	repo          electorRepository
	partition     string // 選主的 Partition Key（例如 "orders:BTC-USD"）
	instanceID    string // 本實例的唯一 ID（建議使用 Pod Name 或 UUID）
	renewInterval time.Duration

	// 當前狀態（goroutine 安全，只在 Run 的 goroutine 內讀寫）
	fencingToken atomic.Int64
	isLeader     atomic.Bool
}

// NewElector 建立一個新的 Elector
func NewElector(repo *Repository, partition, instanceID string) *Elector {
	return &Elector{
		repo:          repo,
		partition:     partition,
		instanceID:    instanceID,
		renewInterval: 5 * time.Second, // 每 5 秒嘗試競選或延長租約（租約 15秒，有 3 個心跳週期的容錯窗口）
	}
}

// IsLeader 回傳目前此實例是否為 Leader（執行緒安全讀取）
func (e *Elector) IsLeader() bool {
	return e.isLeader.Load()
}

// FencingToken 回傳目前的 Fencing Token（呼叫前應確認 IsLeader() 為 true）
func (e *Elector) FencingToken() int64 {
	return e.fencingToken.Load()
}

// Run 啟動選主主迴圈，持續到 ctx 被取消
// 建議在獨立 goroutine 中呼叫：go elector.Run(ctx, onBecomeLeader, onLoseLeadership)
func (e *Elector) Run(
	ctx context.Context,
	onBecomeLeader func(), // 成為 Leader 時的回呼（例如：開始 Consumer 訂閱）
	onLoseLeadership func(), // 失去 Leader 時的回呼（例如：暫停 Consumer 訂閱）
) {
	ticker := time.NewTicker(e.renewInterval)
	defer ticker.Stop()

	logger.Log.Info("LeaderElector 已啟動",
		zap.String("partition", e.partition),
		zap.String("instanceID", e.instanceID),
	)

	for {
		select {
		case <-ctx.Done():
			if e.isLeader.Load() {
				// 優雅關機：主動釋放租約，加速備機接管
				releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				if err := e.repo.ReleaseLock(releaseCtx, e.partition, e.instanceID); err != nil {
					logger.Log.Warn("釋放 Leader 鎖失敗", zap.Error(err))
				}
				cancel()
				e.isLeader.Store(false)
				e.fencingToken.Store(0)
				logger.Log.Info("已釋放 Leader 鎖（優雅關機）", zap.String("partition", e.partition))
			}
			return

		case <-ticker.C:
			e.tick(ctx, onBecomeLeader, onLoseLeadership)
		}
	}
}

// tick 執行一次選主/延租邏輯
func (e *Elector) tick(
	ctx context.Context,
	onBecomeLeader func(),
	onLoseLeadership func(),
) {
	if e.isLeader.Load() {
		// 已是 Leader：嘗試延長租約
		err := e.repo.ExtendLease(ctx, e.partition, e.instanceID, e.fencingToken.Load())
		if err != nil {
			// 延租失敗：代表我們的租約被新 Leader 覆蓋（腦裂保護機制生效）
			logger.Log.Warn("Leader 租約延長失敗，正在退回 Standby",
				zap.String("partition", e.partition),
				zap.Error(err),
			)
			e.isLeader.Store(false)
			e.fencingToken.Store(0)
			metrics.SetPartitionLeader(e.partition, false)
			metrics.ObserveLeaderRenewal("error")
			if onLoseLeadership != nil {
				onLoseLeadership()
			}
		} else {
			metrics.ObserveLeaderRenewal("success")
		}
		return
	}

	// 不是 Leader：嘗試競選
	token, acquired, err := e.repo.AcquireLock(ctx, e.partition, e.instanceID)
	if err != nil {
		logger.Log.Error("竟選 Leader 時發生 DB 錯誤", zap.Error(err))
		return
	}

	if acquired {
		e.isLeader.Store(true)
		e.fencingToken.Store(token)
		metrics.SetPartitionLeader(e.partition, true)
		logger.Log.Info("✅ 成功取得 Leader 鎖",
			zap.String("partition", e.partition),
			zap.String("instanceID", e.instanceID),
			zap.Int64("fencingToken", token),
		)
		if onBecomeLeader != nil {
			onBecomeLeader()
		}
	} else {
		logger.Log.Debug("本次競選未成功，保持 Standby",
			zap.String("partition", e.partition),
		)
	}
}
