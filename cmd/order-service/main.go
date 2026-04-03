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
	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/kafka"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"github.com/RayLiu1999/exchange/internal/infrastructure/outbox"
	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	"github.com/RayLiu1999/exchange/internal/order"
	"github.com/RayLiu1999/exchange/internal/repository"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
)

// order-service: 微服務模式下的訂單管理服務
// 職責：接收 HTTP 下單請求，執行 TX1（鎖定資金 + 建立訂單 + 寫入 Outbox）
// 消費 Kafka：exchange.settlements（TX2 結算寫入 DB）
// 發布 Kafka：由 Outbox Worker 異步從 outbox_messages 讀取並發布 exchange.orders
func main() {
	defer logger.Sync()

	// 1. 資料庫連線（純微服務模式下，Kafka 是必要依賴）
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		logger.Log.Fatal("order-service: DATABASE_URL 環境變數未設定")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		logger.Log.Fatal("order-service: 無法連接資料庫", zap.Error(err))
	}
	defer pool.Close()

	// 2. Repository
	repo := repository.NewPostgresRepository(pool)

	// 2.1 Redis
	redisCfg := redis.DefaultConfig()
	if redisAddr := os.Getenv("REDIS_URL"); redisAddr != "" {
		redisCfg.Addr = redisAddr
	}
	redisClient, err := redis.NewClient(redisCfg)
	if err != nil {
		logger.Log.Warn("Redis 不可用，市價單預估功能將受限", zap.Error(err))
	}
	var cacheRepo domain.CacheRepository
	if redisClient != nil {
		cacheRepo = repository.NewRedisCacheRepository(redisClient)
		logger.Log.Info("Redis 已連線")
	}

	// 2.2 Kafka Producer（純微服務模式：Kafka 連線失敗直接 Fatal，不降級）
	kafkaCfg := kafka.DefaultConfig()
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		kafkaCfg.Brokers = strings.Split(brokers, ",")
	}
	if os.Getenv("GIN_MODE") == "release" {
		kafkaCfg.AllowAutoTopicCreation = false
	}

	kafkaProducer, err := kafka.NewProducer(kafkaCfg)
	if err != nil {
		logger.Log.Fatal("order-service: Kafka 連線失敗，純微服務模式無法啟動", zap.Error(err))
	}
	eventBus := domain.EventPublisher(kafkaProducer)
	logger.Log.Info("Kafka Producer 已連線")

	// 2.3 Outbox Worker（保證 Outbox → Kafka 的可靠傳遞）
	outboxCtx, cancelOutbox := context.WithCancel(context.Background())
	outboxRepo := outbox.NewRepository(pool)
	worker := outbox.NewWorker(outboxRepo, kafkaProducer, 10*time.Second, 100)
	go worker.Start(outboxCtx)

	// 3. ExchangeService（純微服務模式：tradeListener = nil，WebSocket 推播由 market-data-service 負責）
	svc := order.NewService(
		repo, repo, repo, repo, repo,
		cacheRepo,
		eventBus,
		outboxRepo,
	)

	// 4. 啟動 Kafka Consumers
	consumerCtx, cancelConsumers := context.WithCancel(context.Background())
	var settleConsumer *kafka.Consumer

	// 4.1 Settlement consumer（exchange.settlements → TX2 結算寫入 DB）
	settleConsumer, err = kafka.NewConsumer(kafkaCfg, "settlement-engine", []string{domain.TopicSettlements})
	if err != nil {
		logger.Log.Fatal("order-service: 建立 settlement consumer 失敗", zap.Error(err))
	}
	settleConsumer.Start(consumerCtx, svc.HandleEvents)
	logger.Log.Info("Settlement consumer 已啟動", zap.String("topic", domain.TopicSettlements))

	// 5. HTTP 路由（僅供 Gateway 反向代理，安全 Middleware 已在 Gateway 層完成）
	r := gin.Default()
	r.Use(metrics.Middleware("order-service"))
	r.GET("/metrics", gin.WrapH(metrics.Handler()))
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173", "http://localhost:3000"},
		AllowMethods:     []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Idempotency-Key"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))

	handler := api.NewHandler(svc, nil)
	v1 := r.Group("/api/v1")
	handler.RegisterRoutes(v1)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "order-service"})
	})
	r.Static("/docs", "docs")
	url := ginSwagger.URL("http://localhost:8080/docs/swagger.yaml")
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, url))

	// 6. 啟動 HTTP 伺服器
	port := os.Getenv("ORDER_SERVICE_PORT")
	if port == "" {
		port = "8103"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	go func() {
		logger.Info("order-service 已啟動", zap.String("port", ":"+port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("order-service 啟動失敗", zap.Error(err))
		}
	}()

	// 7. 優雅關機
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("order-service 收到關閉訊號")

	cancelConsumers()
	cancelOutbox()
	shutdownDone := make(chan struct{})
	go func() {
		if settleConsumer != nil {
			settleConsumer.Wait()
		}
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		logger.Info("Kafka consumers 已完整關閉")
	case <-time.After(10 * time.Second):
		logger.Warn("Kafka consumer 等待超時，強制繼續關機")
	}

	if kafkaProducer != nil {
		kafkaProducer.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("order-service 強制關閉", zap.Error(err))
	}
	logger.Info("order-service 優雅關機完成")
}
