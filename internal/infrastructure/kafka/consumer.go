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
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics(topics...),
		kgo.DisableAutoCommit(),
		// 從最早的 Offset 開始消費（確保重啟後不遺漏訊息）
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
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
		defer c.wg.Done()
		defer c.client.Close()
		for {
			fetches := c.client.PollFetches(ctx)

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
				for {
					if ctx.Err() != nil {
						return
					}

					if err := handler(ctx, record.Key, record.Value); err != nil {
						logger.Error("Kafka 訊息處理失敗，等待重試",
							zap.String("topic", record.Topic),
							zap.String("group", c.groupID),
							zap.Error(err),
						)
						time.Sleep(1 * time.Second)
						continue
					}

					if err := c.client.CommitRecords(ctx, record); err != nil {
						if ctx.Err() != nil {
							return
						}
						logger.Error("Kafka offset commit 失敗，等待重試",
							zap.String("topic", record.Topic),
							zap.String("group", c.groupID),
							zap.Error(err),
						)
						time.Sleep(1 * time.Second)
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
