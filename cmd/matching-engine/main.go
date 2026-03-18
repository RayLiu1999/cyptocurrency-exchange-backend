package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/RayLiu1999/exchange/internal/infrastructure/kafka"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	"github.com/RayLiu1999/exchange/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// matching-engine: standalone matching service process
// Consumes: exchange.orders (group: matching-engine)
// Publishes: exchange.settlements, exchange.trades, exchange.orderbook
// No API routes (health check only), No WebSocket, No DB writes during runtime
func main() {
	defer logger.Sync()

	// 1. Database connection (read-only at startup: RestoreEngineSnapshot only)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://user:password@localhost:5432/exchange?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		logger.Log.Fatal("matching-engine: cannot connect to DB", zap.Error(err))
	}
	defer pool.Close()

	// 2. Repository (orderRepo used by RestoreEngineSnapshot; others not called by matching-engine)
	repo := repository.NewPostgresRepository(pool)

	// 3. Redis (orderbook cache updates)
	redisCfg := redis.DefaultConfig()
	if redisAddr := os.Getenv("REDIS_URL"); redisAddr != "" {
		redisCfg.Addr = redisAddr
	}
	redisClient, err := redis.NewClient(redisCfg)
	if err != nil {
		logger.Log.Warn("matching-engine: Redis unavailable, orderbook cache disabled", zap.Error(err))
	}
	var cacheRepo core.CacheRepository
	if redisClient != nil {
		cacheRepo = repository.NewRedisCacheRepository(redisClient)
		logger.Log.Info("Redis connected (orderbook cache)")
	}

	// 4. Kafka Producer (publish settlements / trades / orderbook events)
	kafkaCfg := kafka.DefaultConfig()
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		kafkaCfg.Brokers = strings.Split(brokers, ",")
	}
	if os.Getenv("KAFKA_ALLOW_AUTO_CREATE") == "false" {
		kafkaCfg.AllowAutoTopicCreation = false
	} else if os.Getenv("KAFKA_ALLOW_AUTO_CREATE") == "true" {
		kafkaCfg.AllowAutoTopicCreation = true
	}

	producer, err := kafka.NewProducer(kafkaCfg)
	if err != nil {
		logger.Log.Fatal("matching-engine: Kafka producer failed", zap.Error(err))
	}
	defer producer.Close()
	logger.Log.Info("Kafka producer connected")

	// 5. ExchangeService
	//    tradeListener = nil: OnOrderBookUpdate publishes to exchange.orderbook (not in-process WS)
	svc := core.NewExchangeService(
		repo, repo, repo, repo, repo,
		"BTC-USD",
		nil,
		cacheRepo,
		producer,
	)

	// 6. Restore engine snapshot (must complete before consumer starts)
	logger.Log.Info("Restoring engine snapshot from DB...")
	if err := svc.RestoreEngineSnapshot(context.Background()); err != nil {
		logger.Log.Error("RestoreEngineSnapshot failed", zap.Error(err))
	}
	logger.Log.Info("Engine snapshot restored")

	// 6a. Create required Kafka topics before publishing (避免 UNKNOWN_TOPIC_OR_PARTITION)
	topicCtx, topicCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer topicCancel()
	if err := producer.CreateTopics(topicCtx, []string{
		core.TopicOrders,
		core.TopicSettlements,
		core.TopicTrades,
		core.TopicOrderBook,
		core.TopicOrderUpdates,
	}); err != nil {
		logger.Log.Warn("CreateTopics 失敗（主題可能已存在）", zap.Error(err))
	} else {
		logger.Log.Info("Kafka topics initialized")
	}

	// 6b. Warm up Redis orderbook cache from in-memory engine snapshot
	// 讓 order-service 啟動後能立即讀取 Redis 快取估算市價單所需資金
	if cacheRepo != nil {
		if _, err := svc.GetOrderBook(context.Background(), "BTC-USD"); err != nil {
			logger.Log.Warn("Redis orderbook cache warmup failed", zap.Error(err))
		} else {
			logger.Log.Info("Redis orderbook cache warmed up")
		}
	}

	// 7. Start Kafka consumer (exchange.orders)
	consumerCtx, cancelConsumers := context.WithCancel(context.Background())
	defer cancelConsumers()

	matchConsumer, err := kafka.NewConsumer(kafkaCfg, "matching-engine", []string{core.TopicOrders})
	if err != nil {
		logger.Log.Fatal("Failed to create matching consumer", zap.Error(err))
	}
	matchConsumer.Start(consumerCtx, svc.HandleMatchingEvent)
	logger.Log.Info("Kafka matching consumer started", zap.String("topic", core.TopicOrders))

	// 8. Health check HTTP endpoint (ECS ALB / Docker probe)
	r := gin.New()
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "matching-engine"})
	})
	port := os.Getenv("MATCHING_ENGINE_PORT")
	if port == "" {
		port = "8081"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	go func() {
		logger.Log.Info("Health check server started on :" + port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Error("Health check server error", zap.Error(err))
		}
	}()

	// 9. Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Log.Info("Shutdown signal received", zap.String("signal", sig.String()))

	cancelConsumers()
	matchConsumer.Wait()

	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(ctxShutdown); err != nil {
		logger.Log.Error("Health check server shutdown error", zap.Error(err))
	}
	logger.Log.Info("matching-engine shutdown complete")
}
