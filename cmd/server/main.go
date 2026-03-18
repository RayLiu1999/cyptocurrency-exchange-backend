package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RayLiu1999/exchange/internal/api"
	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/RayLiu1999/exchange/internal/infrastructure/kafka"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger" // 使用您自訂的 Logger
	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	"github.com/RayLiu1999/exchange/internal/middleware"
	"github.com/RayLiu1999/exchange/internal/repository"
	"github.com/RayLiu1999/exchange/internal/simulator"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
)

func main() {
	defer logger.Sync()

	// 1. 資料庫連線
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://user:password@localhost:5432/exchange?sslmode=disable"
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		logger.Log.Fatal("無法連接資料庫", zap.Error(err))
	}
	defer pool.Close()

	// 自動執行 Migrations
	logger.Log.Info("📦 正在執行資料庫 Migration...")
	schemaBytes, err := os.ReadFile("sql/schema.sql")
	if err != nil {
		logger.Log.Warn("找不到 sql/schema.sql 文件，跳過自動 Migration", zap.Error(err))
	} else {
		if _, err := pool.Exec(context.Background(), string(schemaBytes)); err != nil {
			logger.Log.Fatal("❌ 資料庫 Migration 失敗", zap.Error(err))
		}
		logger.Log.Info("✅ 資料庫 Migration 完成")
	}

	// 2. Repository
	repo := repository.NewPostgresRepository(pool)

	// 2.1 Redis Client 與 Cache Repository
	redisCfg := redis.DefaultConfig()
	if redisAddr := os.Getenv("REDIS_URL"); redisAddr != "" {
		redisCfg.Addr = redisAddr
	}
	redisClient, err := redis.NewClient(redisCfg)
	if err != nil {
		logger.Log.Warn("Redis 連線失敗，系統將以 Memory Fallback 模式運作", zap.Error(err))
		// 不 panic，允許系統無 Redis 啟動
	}
	var cacheRepo core.CacheRepository
	if redisClient != nil {
		cacheRepo = repository.NewRedisCacheRepository(redisClient)
	}

	// 2.2 Kafka Producer (可選：Kafka 不可用時系統退回同步撮合模式)
	kafkaCfg := kafka.DefaultConfig()
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		// 支援多個 broker 以逗號分隔，例如："b1:9092,b2:9092"
		kafkaCfg.Brokers = strings.Split(brokers, ",")
	}
	// 生產環境應關閉 AllowAutoTopicCreation (環境變數為 "true" 才開啟)
	if os.Getenv("KAFKA_ALLOW_AUTO_CREATE") == "false" {
		kafkaCfg.AllowAutoTopicCreation = false
	} else if os.Getenv("KAFKA_ALLOW_AUTO_CREATE") == "true" {
		kafkaCfg.AllowAutoTopicCreation = true
	} else if os.Getenv("GO_ENV") == "production" {
		// 根據環境變數安全退避：GO_ENV=production 時由 DefaultConfig 的 true 改為 false
		kafkaCfg.AllowAutoTopicCreation = false
	}

	var eventBus core.EventPublisher
	var kafkaProducer *kafka.Producer
	if producer, perr := kafka.NewProducer(kafkaCfg); perr != nil {
		logger.Log.Warn("Kafka 連線失敗，系統將以同步撮合模式運作", zap.Error(perr))
	} else {
		kafkaProducer = producer
		eventBus = producer
		logger.Log.Info("✅ Kafka Producer 已連線")
	}

	// 3. WebSocket Handler (先建立，作為事件監聽者)
	wsHandler := api.NewWebSocketHandler()
	go wsHandler.Run()
	// wsHandler.StartBroadcastingDummyData() // 已移除，改用 Real Data

	// 4. Service (內建撮合引擎，注入 repo 作為所有的 Repository 實現)
	svc := core.NewExchangeService(repo, repo, repo, repo, repo, "BTC-USD", wsHandler, cacheRepo, eventBus)

	// 啟動時從資料庫還原未完成的訂單，重建掛單簿
	// ⚠️ 必須在 Kafka Consumers 啟動前完成，防止 Cold Start 空掛單簿問題
	if err := svc.RestoreEngineSnapshot(context.Background()); err != nil {
		logger.Log.Error("還原撮合引擎快照失敗", zap.Error(err))
	}

	// 啟動 Kafka Consumers（必須在 RestoreEngineSnapshot 完成後）
	consumerCtx, cancelConsumers := context.WithCancel(context.Background())
	var matchConsumer *kafka.Consumer
	var settleConsumer *kafka.Consumer
	var merr error
	var serr error
	if eventBus != nil {
		matchConsumer, merr = kafka.NewConsumer(kafkaCfg, "matching-engine", []string{core.TopicOrders})
		if merr != nil {
			logger.Log.Error("建立 Kafka matching consumer 失敗", zap.Error(merr))
		} else {
			matchConsumer.Start(consumerCtx, svc.HandleMatchingEvent)
		}

		settleConsumer, serr = kafka.NewConsumer(kafkaCfg, "settlement-engine", []string{core.TopicSettlements})
		if serr != nil {
			logger.Log.Error("建立 Kafka settlement consumer 失敗", zap.Error(serr))
		} else {
			settleConsumer.Start(consumerCtx, svc.HandleSettlementEvent)
		}
		logger.Log.Info("✅ Kafka Consumers 已啟動 (matching + settlement)")
	}

	// 4-1. Simulator
	sim := simulator.NewService(svc)

	// 6. 啟動伺服器
	r := gin.Default()

	// CORS：只允許白名單 Origin（防 CSRF），加入 Idempotency-Key Header 許可
	corsConfig := cors.Config{
		AllowOrigins: []string{
			"http://localhost:5173", // 本地前端開發
			"http://localhost:3000",
		},
		AllowMethods:     []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Idempotency-Key"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}
	r.Use(cors.New(corsConfig))

	// ========== 初始化安全性 Middleware ==========
	// 若有 Redis 則使用分散式實作；若無則 Fallback 至單機 Memory 實作
	var publicLimiter, privateLimiter middleware.RateLimiter
	var idempStore middleware.IdempotencyStore

	if redisClient != nil {
		publicLimiter = middleware.NewRedisRateLimiter(redisClient, 60, time.Minute)    // 60 次/分鐘
		privateLimiter = middleware.NewRedisRateLimiter(redisClient, 10, 1*time.Second) // 10 次/秒
		idempStore = middleware.NewRedisIdempotencyStore(redisClient)
		logger.Log.Info("✅ 安全性 Middleware 架構：[分散式 Redis 模式]")
	} else {
		publicLimiter = middleware.NewMemoryRateLimiter(1, 60, 10*time.Minute)   // 60 次/分鐘 (Burst: 1)
		privateLimiter = middleware.NewMemoryRateLimiter(10, 10, 10*time.Minute) // 10 次/秒 (Burst: 10)
		idempStore = middleware.NewMemoryIdempotencyStore()
		logger.Log.Warn("⚠️ 安全性 Middleware 架構：[單機 Memory 模式]")
	}

	// API v1 Routing Group，掛載 5. HTTP Handler
	handler := api.NewHandler(svc, sim)
	v1 := r.Group("/api/v1")
	handler.RegisterRoutesWithMiddleware(v1, publicLimiter, privateLimiter, idempStore)

	// Health Check（ALB 與 ECS 健康檢查用，不需要驗證或限流）
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// WebSocket Route
	r.GET("/ws", wsHandler.HandleWS)

	// Swagger Documentation
	r.Static("/docs", "docs")
	url := ginSwagger.URL("http://localhost:8080/docs/swagger.yaml")
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, url))

	// 6. 啟動伺服器 (Graceful Shutdown 實作)
	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,
	}

	go func() {
		logger.Info("🚀 伺服器啟動", zap.String("port", ":8080"))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("伺服器啟動失敗", zap.Error(err))
		}
	}()

	// 等待中斷訊號
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("正在關閉伺服器...")

	// 停止 Kafka Consumers，並等待 worker 完整結束後再關閉 Producer，避免關機時遺失 in-flight 事件。
	// 設定最長等待 10 秒，防止 consumer 卡在 retry 迴圈時 Server 永久阻塞。
	cancelConsumers()
	shutdownDone := make(chan struct{})
	go func() {
		if matchConsumer != nil {
			matchConsumer.Wait()
		}
		if settleConsumer != nil {
			settleConsumer.Wait()
		}
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		logger.Info("Kafka Consumers 已完整關閉")
	case <-time.After(10 * time.Second):
		logger.Warn("Kafka Consumers 等待超時，強制繼續關機")
	}
	if kafkaProducer != nil {
		kafkaProducer.Close()
	}

	// 設定超時時間，等待當前請求處理完畢
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("伺服器強制關閉", zap.Error(err))
	}

	logger.Info("伺服器已優雅關閉")
}
