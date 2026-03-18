package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"go.uber.org/zap"
)

// Producer 封裝 Kafka 生產者
type Producer struct {
	client         *kgo.Client
	publishTimeout time.Duration
}

// NewProducer 建立 Kafka 生產者
func NewProducer(cfg Config) (*Producer, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
	}

	// 僅在設定允許時啟用自動建立 Topic (通常僅用於開發環境)
	if cfg.AllowAutoTopicCreation {
		opts = append(opts, kgo.AllowAutoTopicCreation())
	}

	client, err := kgo.NewClient(opts...)
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
	return &Producer{client: client, publishTimeout: cfg.PublishTimeout}, nil
}

// Publish 將 payload 序列化為 JSON 後發布至指定 topic
// key 用於決定 Partition（同一 key 保證有序）
func (p *Producer) Publish(ctx context.Context, topic, key string, payload interface{}) error {
	pubCtx, cancel := context.WithTimeout(ctx, p.publishTimeout)
	defer cancel()

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化事件失敗 (topic: %s): %w", topic, err)
	}

	record := &kgo.Record{
		Topic: topic,
		Key:   []byte(key),
		Value: data,
	}

	if err := p.client.ProduceSync(pubCtx, record).FirstErr(); err != nil {
		logger.Error("發布 Kafka 事件失敗",
			zap.String("topic", topic),
			zap.String("key", key),
			zap.Error(err),
		)
		return fmt.Errorf("發布事件失敗: %w", err)
	}

	return nil
}

// CreateTopics 建立指定 Kafka 主題（若已存在則忽略）
// 使用 kmsg 直接透過現有 kgo.Client 送出 CreateTopicsRequest，無需額外 Admin Client。
// 適合服務啟動時確保所有 topic 存在，避免 UNKNOWN_TOPIC_OR_PARTITION 錯誤。
func (p *Producer) CreateTopics(ctx context.Context, topics []string) error {
	req := &kmsg.CreateTopicsRequest{
		TimeoutMillis: 10000,
		Topics:        make([]kmsg.CreateTopicsRequestTopic, 0, len(topics)),
	}
	for _, topic := range topics {
		req.Topics = append(req.Topics, kmsg.CreateTopicsRequestTopic{
			Topic:             topic,
			NumPartitions:     1,
			ReplicationFactor: 1,
		})
	}

	kresp, err := p.client.Request(ctx, req)
	if err != nil {
		return fmt.Errorf("CreateTopics 請求失敗: %w", err)
	}

	resp := kresp.(*kmsg.CreateTopicsResponse)
	for _, t := range resp.Topics {
		// ErrorCode 36 = TOPIC_ALREADY_EXISTS，視為成功
		if t.ErrorCode != 0 && t.ErrorCode != 36 {
			logger.Log.Warn("建立 Kafka topic 失敗（可能已存在）",
				zap.String("topic", t.Topic),
				zap.Int16("errorCode", t.ErrorCode),
			)
		}
	}
	return nil
}

// Close 關閉生產者
func (p *Producer) Close() {
	p.client.Close()
}
