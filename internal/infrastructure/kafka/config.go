package kafka

import "time"

// Config Kafka 基礎設定
type Config struct {
	Brokers                []string
	ConnectTimeout         time.Duration
	PublishTimeout         time.Duration
	AllowAutoTopicCreation bool // 只有開發環境應開啟，生產環境應關閉
}

// DefaultConfig 返回本地開發預設設定
func DefaultConfig() Config {
	return Config{
		Brokers:                []string{"localhost:9092"},
		ConnectTimeout:         5 * time.Second,
		PublishTimeout:         2 * time.Second,
		AllowAutoTopicCreation: true, // 本地開發預設開啟
	}
}
