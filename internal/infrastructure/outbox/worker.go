package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type workerRepository interface {
	CountPending(ctx context.Context) (int64, error)
	WithTx(ctx context.Context, fn func(context.Context) error) error
	FetchPending(ctx context.Context, batchSize int, gracePeriod time.Duration) ([]*Message, error)
	IncrementRetry(ctx context.Context, id uuid.UUID) error
	MarkPublishedBatch(ctx context.Context, ids []uuid.UUID) error
}

var _ workerRepository = (*Repository)(nil)

// Publisher 定義將訊息發布到 Kafka 的介面，解耦 Worker 與 Kafka 實作
type Publisher interface {
	PublishRaw(ctx context.Context, topic, partitionKey string, value []byte) error
}

// Worker 是 Outbox 模式的核心元件
// 它定期掃描 outbox_messages 表，將 Pending 訊息發送到 Kafka 並標記為 Published
// 設計保證：即使主流程的 Kafka.Publish() 失敗，訊息也不會遺失
type Worker struct {
	repo      workerRepository
	publisher Publisher
	interval  time.Duration
	batchSize int
}

// NewWorker 建立一個新的 Outbox Worker
func NewWorker(repo *Repository, publisher Publisher, interval time.Duration, batchSize int) *Worker {
	return &Worker{
		repo:      repo,
		publisher: publisher,
		interval:  interval,  // 定時掃描間隔
		batchSize: batchSize, // 每批處理數量
	}
}

// Start 在 goroutine 中啟動 Worker，直到 ctx 被取消
func (w *Worker) Start(ctx context.Context) {
	logger.Log.Info("Outbox Worker 已啟動", zap.Duration("interval", w.interval))
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Log.Info("Outbox Worker 收到關閉訊號，正在停止")
			return
		case <-ticker.C:
			w.process(ctx)
		}
	}
}

// process 執行一次批次掃描與發送
func (w *Worker) process(ctx context.Context) {
	// 更新積壓量指標，用於 Grafana Alert 監控
	if pending, err := w.repo.CountPending(ctx); err == nil {
		metrics.SetOutboxPendingCount(float64(pending))
	}

	err := w.repo.WithTx(ctx, func(txCtx context.Context) error {
		// 將抓取、重試遞增與刪除放在同一個 DB Transaction 內，
		// 讓 FOR UPDATE SKIP LOCKED 的鎖能持續到整批處理結束。
		msgs, err := w.repo.FetchPending(txCtx, w.batchSize, 5*time.Second)
		if err != nil {
			return fmt.Errorf("讀取 pending 訊息失敗: %w", err)
		}

		successfulIDs := make([]uuid.UUID, 0, len(msgs))
		for _, msg := range msgs {
			start := time.Now()
			publishErr := w.publisher.PublishRaw(ctx, msg.Topic, msg.PartitionKey, msg.Payload)
			latency := time.Since(start)

			if publishErr != nil {
				logger.Log.Warn("Outbox Worker 發送訊息到 Kafka 失敗",
					zap.String("id", msg.ID.String()),
					zap.String("topic", msg.Topic),
					zap.Error(publishErr),
				)
				if err := w.repo.IncrementRetry(txCtx, msg.ID); err != nil {
					return fmt.Errorf("增加 retry_count 失敗: %w", err)
				}
				metrics.ObserveOutboxPublish("error", latency)
				continue
			}

			successfulIDs = append(successfulIDs, msg.ID)
			metrics.ObserveOutboxPublish("success", latency)
		}

		if len(successfulIDs) == 0 {
			return nil
		}

		if err := w.repo.MarkPublishedBatch(txCtx, successfulIDs); err != nil {
			return fmt.Errorf("批次標記 published 失敗: %w", err)
		}

		logger.Log.Debug("Outbox Worker 批次刪除成功", zap.Int("count", len(successfulIDs)))
		return nil
	})
	if err != nil {
		logger.Log.Error("Outbox Worker 處理批次失敗", zap.Error(err))
	}
}

// MarshalPayload 將任意 struct 序列化為 JSON []byte，供業務層呼叫
func MarshalPayload(v any) ([]byte, error) {
	return json.Marshal(v)
}
