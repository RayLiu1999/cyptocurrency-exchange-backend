package kafka

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
)

// Producer 封裝 Kafka 生產者
type Producer struct {
	client *kgo.Client
}

// NewProducer 建立 Kafka 生產者
func NewProducer(cfg Config) (*Producer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		// 啟用自動建立 Topic (針對本地開發/Redpanda 環境)
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return nil, fmt.Errorf("建立 Kafka Producer 失敗: %w", err)
	}

	// 確認 Broker 可達
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ConnectTimeout)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("Kafka Broker 連線失敗: %w", err)
	}

	logger.Info("✅ Kafka Producer 連線成功", zap.Strings("brokers", cfg.Brokers))
	return &Producer{client: client}, nil
}

// Publish 將 payload 序列化為 JSON 後發布至指定 topic
// key 用於決定 Partition（同一 key 保證有序）
func (p *Producer) Publish(ctx context.Context, topic, key string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化事件失敗 (topic: %s): %w", topic, err)
	}

	record := &kgo.Record{
		Topic: topic,
		Key:   []byte(key),
		Value: data,
	}

	if err := p.client.ProduceSync(ctx, record).FirstErr(); err != nil {
		logger.Error("發布 Kafka 事件失敗",
			zap.String("topic", topic),
			zap.String("key", key),
			zap.Error(err),
		)
		return fmt.Errorf("發布事件失敗: %w", err)
	}

	return nil
}

// Close 關閉生產者
func (p *Producer) Close() {
	p.client.Close()
}
