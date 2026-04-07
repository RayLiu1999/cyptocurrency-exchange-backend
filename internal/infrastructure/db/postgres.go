package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPostgresPool 根據傳入的 DBConfig 初始化 pgx 連線池
func NewPostgresPool(ctx context.Context, cfg DBConfig) (*pgxpool.Pool, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("資料庫 URL 未設定")
	}

	// 透過 ParseConfig 解析連線字串
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("解析資料庫配置失敗: %w", err)
	}

	// 覆蓋為我們自定義的連線池參數
	if cfg.MaxOpenConns > 0 {
		poolConfig.MaxConns = int32(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleTime > 0 {
		poolConfig.MaxConnIdleTime = cfg.MaxIdleTime
	}
	if cfg.MaxLifeTime > 0 {
		poolConfig.MaxConnLifetime = cfg.MaxLifeTime
	}

	// 建立連線池
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("建立資料庫連線池失敗: %w", err)
	}

	return pool, nil
}
