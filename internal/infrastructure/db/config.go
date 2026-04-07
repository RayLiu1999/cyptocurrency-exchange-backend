package db

import (
	"time"
)

type DBConfig struct {
	URL          string
	MaxOpenConns int           // pgx Config.MaxConns
	MinOpenConns int           // pgx Config.MinConns (取代 MaxIdleConns)
	MaxIdleTime  time.Duration // pgx Config.MaxConnIdleTime
	MaxLifeTime  time.Duration // pgx Config.MaxConnLifetime
}

func DefaultDBConfig(url string) DBConfig {
	return DBConfig{
		URL:          url,
		MaxOpenConns: 50, // 100 有點保守，對於交易所高併發場景核心服務可設 50-100
		MinOpenConns: 5,  // 保持 5 條連線熱機，減少冷啟動延遲
		MaxIdleTime:  5 * time.Minute,
		MaxLifeTime:  1 * time.Hour,
	}
}
