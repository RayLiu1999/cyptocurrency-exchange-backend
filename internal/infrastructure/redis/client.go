package redis

import (
	"context"
	"strings"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Client 封裝 go-redis 實例
type Client struct {
	Client *redis.Client
}

// Config Redis 連線設定
type Config struct {
	Addr         string        // 可為 Redis 位址 (如 localhost:6379) 或完整的 REDIS_URL (如 redis://...)
	Password     string        // 密碼 (無則留空，若使用 URL 則會被 URL 內的密碼覆蓋)
	DB           int           // 資料庫編號 (若使用 URL 則會被 URL 內的 DB 覆蓋)
	PoolSize     int           // 最大連線數
	MinIdleConns int           // 最小閒置連線數
	ReadTimeout  time.Duration // 讀取超時
	WriteTimeout time.Duration // 寫入超時
}

// DefaultConfig 回傳預設配置
func DefaultConfig() Config {
	return Config{
		Addr:         "localhost:6379",
		Password:     "",
		DB:           0,
		PoolSize:     100,
		MinIdleConns: 20,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}
}

// NewClient 建立並初始化 Redis 連線池
func NewClient(cfg Config) (*Client, error) {
	var opt *redis.Options
	var err error

	// 支援「混合模式」：有 redis:// 則視為 URL 解析
	if strings.HasPrefix(cfg.Addr, "redis://") || strings.HasPrefix(cfg.Addr, "rediss://") {
		opt, err = redis.ParseURL(cfg.Addr)
		if err != nil {
			return nil, err
		}
		// 套用進階連線池設定
		opt.PoolSize = cfg.PoolSize
		opt.MinIdleConns = cfg.MinIdleConns
		opt.ReadTimeout = cfg.ReadTimeout
		opt.WriteTimeout = cfg.WriteTimeout
	} else {
		// 傳統 Address 模式
		opt = &redis.Options{
			Addr:         cfg.Addr,
			Password:     cfg.Password,
			DB:           cfg.DB,
			PoolSize:     cfg.PoolSize,
			MinIdleConns: cfg.MinIdleConns,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
		}
	}

	rdb := redis.NewClient(opt)

	// 測試連線
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Error("❌ 無法連線至 Redis", zap.Error(err), zap.String("addr", cfg.Addr))
		return nil, err
	}

	logger.Info("✅ Redis 連線成功", zap.String("addr", cfg.Addr), zap.Int("pool_size", cfg.PoolSize))
	return &Client{Client: rdb}, nil
}

// Close 關閉連線池
func (c *Client) Close() error {
	if c.Client != nil {
		return c.Client.Close()
	}
	return nil
}
