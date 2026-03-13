package kafka

import "time"

// Config Kafka 基礎設定
type Config struct {
	Brokers        []string
	ConnectTimeout time.Duration
}

// DefaultConfig 返回本地開發預設設定
func DefaultConfig() Config {
	return Config{
		Brokers:        []string{"localhost:9092"},
		ConnectTimeout: 5 * time.Second,
	}
}
