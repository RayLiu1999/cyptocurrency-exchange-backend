package kafka

import (
	"context"
	"sync"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
)

// HandlerFunc 訊息處理函式，key 和 value 為原始位元組
type HandlerFunc func(ctx context.Context, key, value []byte) error

// Consumer 封裝 Kafka 消費者
type Consumer struct {
	client  *kgo.Client
	groupID string
	topics  []string
	wg      sync.WaitGroup
}

// NewConsumer 建立 Kafka 消費者
func NewConsumer(cfg Config, groupID string, topics []string) (*Consumer, error) {
	resetOffset := kgo.NewOffset().AtEnd() // 預設從 latest 開始消費，避免重放舊事件造成問題
	switch cfg.ResetOffset {
	case "", "latest":
		resetOffset = kgo.NewOffset().AtEnd()
	case "earliest":
		resetOffset = kgo.NewOffset().AtStart()
	default:
		logger.Warn("未知的 Kafka reset offset 設定，改用 latest",
			zap.String("group", groupID),
			zap.String("reset_offset", cfg.ResetOffset),
		)
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...), // Kafka broker 地址
		kgo.ConsumerGroup(groupID),      // 消費者群組 ID
		kgo.ConsumeTopics(topics...),    // 消費的 topic
		kgo.DisableAutoCommit(),         // 關閉自動 commit
		// 若 consumer group 沒有 committed offset，預設從 latest 開始，避免 DB 已有狀態時重放舊事件造成鬼單/重複結算。
		kgo.ConsumeResetOffset(resetOffset), // 重置 offset
	)
	if err != nil {
		return nil, err
	}

	return &Consumer{
		client:  client,
		groupID: groupID,
		topics:  topics,
	}, nil
}

// Start 在 Goroutine 中持續輪詢並處理訊息
// 當 ctx 被取消時，自動停止並關閉 Consumer
func (c *Consumer) Start(ctx context.Context, handler HandlerFunc) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()      // 確保 Wait() 能正常工作
		defer c.client.Close() // 確保 client 能正常關閉
		for {
			fetches := c.client.PollFetches(ctx) // 輪詢 Kafka，獲取新訊息

			// Context 被取消，優雅退出
			if ctx.Err() != nil {
				logger.Info("Kafka Consumer 已停止", zap.String("group", c.groupID))
				return
			}

			if errs := fetches.Errors(); len(errs) > 0 {
				for _, e := range errs {
					logger.Error("Kafka 拉取訊息失敗",
						zap.String("group", c.groupID),
						zap.Error(e.Err),
					)
				}
				continue
			}

			fetches.EachRecord(func(record *kgo.Record) {
				backoff := 100 * time.Millisecond // 初始 backoff 時間
				for {
					if ctx.Err() != nil {
						return
					}

					if err := handler(ctx, record.Key, record.Value); err != nil {
						logger.Error("Kafka 訊息處理失敗，等待重試",
							zap.String("topic", record.Topic),
							zap.String("group", c.groupID),
							zap.Duration("backoff", backoff),
							zap.Error(err),
						)

						select {
						case <-time.After(backoff):
							backoff *= 2
							if backoff > 30*time.Second {
								backoff = 30 * time.Second
							}
						case <-ctx.Done():
							return
						}
						continue
					}

					if err := c.client.CommitRecords(ctx, record); err != nil {
						if ctx.Err() != nil {
							return
						}
						logger.Error("Kafka offset commit 失敗，等待重試",
							zap.String("topic", record.Topic),
							zap.String("group", c.groupID),
							zap.Duration("backoff", backoff),
							zap.Error(err),
						)

						select {
						case <-time.After(backoff):
							backoff *= 2
							if backoff > 30*time.Second {
								backoff = 30 * time.Second
							}
						case <-ctx.Done():
							return
						}
						continue
					}

					break
				}
			})
		}
	}()
}

// Wait 等待 Consumer goroutine 完整結束，確保關機時不會與 Producer.Close 競態。
func (c *Consumer) Wait() {
	c.wg.Wait()
}
