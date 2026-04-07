package outbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Publisher 定義將訊息發布到 Kafka 的介面，解耦 Worker 與 Kafka 實作
type Publisher interface {
	PublishRaw(ctx context.Context, topic, partitionKey string, value []byte) error
}

// Worker 是 Outbox 模式的核心元件
// 它定期掃描 outbox_messages 表，將 Pending 訊息發送到 Kafka 並標記為 Published
// 設計保證：即使主流程的 Kafka.Publish() 失敗，訊息也不會遺失
type Worker struct {
	repo      *Repository
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

	// 加上 5 秒冷靜期，避免搶到剛被熱路徑建立、且正在發送中的事件
	msgs, err := w.repo.FetchPending(ctx, w.batchSize, 5*time.Second)
	if err != nil {
		logger.Log.Error("Outbox Worker 讀取 Pending 訊息失敗", zap.Error(err))
		return
	}

	var successfulIDs []uuid.UUID

	for _, msg := range msgs {
		start := time.Now()
		publishErr := w.publisher.PublishRaw(ctx, msg.Topic, msg.PartitionKey, msg.Payload)
		latency := time.Since(start)

		if publishErr != nil {
			// 發送失敗：增加重試計數，等待下次掃描重試
			logger.Log.Warn("Outbox Worker 發送訊息到 Kafka 失敗",
				zap.String("id", msg.ID.String()),
				zap.String("topic", msg.Topic),
				zap.Error(publishErr),
			)
			_ = w.repo.IncrementRetry(ctx, msg.ID)
			metrics.ObserveOutboxPublish("error", latency)
			continue
		}

		// 發送成功：收集 ID 等待批次刪除
		successfulIDs = append(successfulIDs, msg.ID)
		metrics.ObserveOutboxPublish("success", latency)
	}

	// 批次標記已發布（物理刪除）
	if len(successfulIDs) > 0 {
		if markErr := w.repo.MarkPublishedBatch(ctx, successfulIDs); markErr != nil {
			logger.Log.Error("Outbox Worker 批次標記 Published 失敗",
				zap.Int("count", len(successfulIDs)),
				zap.Error(markErr),
			)
		} else {
			logger.Log.Debug("Outbox Worker 批次刪除成功", zap.Int("count", len(successfulIDs)))
		}
	}
}

// MarshalPayload 將任意 struct 序列化為 JSON []byte，供業務層呼叫
func MarshalPayload(v any) ([]byte, error) {
	return json.Marshal(v)
}
