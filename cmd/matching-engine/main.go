package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/kafka"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	"github.com/RayLiu1999/exchange/internal/matching"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"github.com/RayLiu1999/exchange/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// matching-engine: 獨立的記憶體撮合引擎服務
// 消費 Kafka：exchange.orders（group: matching-engine）
// 發布 Kafka：exchange.settlements, exchange.trades, exchange.orderbook
// 無對外 API（僅 health check），無 WebSocket，執行期間無 DB 寫入
func main() {
	defer logger.Sync()

	// 1. 資料庫連線（唯讀，僅用於啟動時 RestoreEngineSnapshot）
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		logger.Log.Fatal("matching-engine: DATABASE_URL 環境變數未設定")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		logger.Log.Fatal("matching-engine: 無法連接資料庫", zap.Error(err))
	}
	defer pool.Close()

	// 2. Repository（僅供 RestoreEngineSnapshot 使用）
	repo := repository.NewPostgresRepository(pool)

	// 3. Redis（掛單簿快取更新）
	redisCfg := redis.DefaultConfig()
	if redisAddr := os.Getenv("REDIS_URL"); redisAddr != "" {
		redisCfg.Addr = redisAddr
	}
	redisClient, err := redis.NewClient(redisCfg)
	if err != nil {
		logger.Log.Warn("matching-engine: Redis 不可用，掛單簿快取已停用", zap.Error(err))
	}
	var cacheRepo domain.CacheRepository
	if redisClient != nil {
		cacheRepo = repository.NewRedisCacheRepository(redisClient)
		logger.Log.Info("Redis 已連線（掛單簿快取）")
	}

	// 4. Kafka Producer（發布 settlements / trades / orderbook 事件）
	kafkaCfg := kafka.DefaultConfig()
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		kafkaCfg.Brokers = strings.Split(brokers, ",")
	}
	if resetOffset := os.Getenv("KAFKA_RESET_OFFSET"); resetOffset != "" {
		kafkaCfg.ResetOffset = strings.ToLower(resetOffset)
	}
	if os.Getenv("KAFKA_ALLOW_AUTO_CREATE") == "false" {
		kafkaCfg.AllowAutoTopicCreation = false
	} else if os.Getenv("KAFKA_ALLOW_AUTO_CREATE") == "true" {
		kafkaCfg.AllowAutoTopicCreation = true
	}

	producer, err := kafka.NewProducer(kafkaCfg)
	if err != nil {
		logger.Log.Fatal("matching-engine: Kafka Producer 連線失敗", zap.Error(err))
	}
	defer producer.Close()
	logger.Log.Info("Kafka Producer 已連線")

	// 5. Service
	engineManager := engine.NewEngineManager()
	svc := matching.NewSubscriber(engineManager, producer, cacheRepo)

	// 6. 冷啟動：從 DB 還原活動訂單至記憶體引擎
	logger.Log.Info("正在從 DB 還原撮合引擎快照...")
	if err := matching.RestoreEngineSnapshot(context.Background(), repo, engineManager); err != nil {
		logger.Log.Error("RestoreEngineSnapshot 失敗", zap.Error(err))
	}
	logger.Log.Info("撮合引擎快照還原完成")

	// 6a. 預先建立 Kafka Topics（避免 UNKNOWN_TOPIC_OR_PARTITION）
	topicCtx, topicCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer topicCancel()
	if err := producer.CreateTopics(topicCtx, []string{
		domain.TopicOrders,
		domain.TopicSettlements,
		domain.TopicTrades,
		domain.TopicOrderBook,
		domain.TopicOrderUpdates,
	}); err != nil {
		logger.Log.Warn("CreateTopics 失敗（主題可能已存在）", zap.Error(err))
	} else {
		logger.Log.Info("Kafka topics 已初始化")
	}

	restoredSymbols := svc.SyncRecoveredOrderBooks(20)
	if len(restoredSymbols) == 0 {
		logger.Log.Info("冷啟動後沒有需要同步的掛單簿快照")
	} else {
		logger.Log.Info("冷啟動掛單簿快照已同步至 Redis 與市場資料流",
			zap.Strings("symbols", restoredSymbols),
		)
	}

	// 7. 啟動 Kafka Consumer（exchange.orders）
	consumerCtx, cancelConsumers := context.WithCancel(context.Background())
	defer cancelConsumers()

	matchConsumer, err := kafka.NewConsumer(kafkaCfg, "matching-engine", []string{domain.TopicOrders})
	if err != nil {
		logger.Log.Fatal("matching-engine: 建立 matching consumer 失敗", zap.Error(err))
	}
	matchConsumer.Start(consumerCtx, svc.HandleEvents)
	logger.Log.Info("Kafka matching consumer 已啟動", zap.String("topic", domain.TopicOrders))

	// 8. Health check + metrics 端點（供 ECS ALB / Docker probe）
	r := gin.New()
	r.Use(metrics.Middleware("matching-engine"))
	r.GET("/metrics", gin.WrapH(metrics.Handler()))
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "matching-engine"})
	})
	port := os.Getenv("MATCHING_ENGINE_PORT")
	if port == "" {
		port = "8101"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	go func() {
		logger.Log.Info("Health check server 已啟動", zap.String("port", ":"+port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Error("Health check server 發生錯誤", zap.Error(err))
		}
	}()

	// 9. 優雅關機
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Log.Info("收到關閉訊號", zap.String("signal", sig.String()))

	cancelConsumers()
	matchConsumer.Wait()

	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(ctxShutdown); err != nil {
		logger.Log.Error("Health check server 關閉失敗", zap.Error(err))
	}
	logger.Log.Info("matching-engine 優雅關機完成")
}
