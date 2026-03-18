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
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	"github.com/RayLiu1999/exchange/internal/repository"
	"github.com/RayLiu1999/exchange/internal/simulator"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
)

// order-service: standalone order management service
// Consumes: exchange.settlements (TX2 DB writes)
// Publishes: exchange.orders (after TX1 fund lock)
// Hosts: HTTP API
func main() {
	defer logger.Sync()

	// 1. Database connection
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://user:password@localhost:5432/exchange?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		logger.Log.Fatal("order-service: cannot connect to DB", zap.Error(err))
	}
	defer pool.Close()

	// Auto-run migrations (order-service owns schema init as HTTP entry point)
	logger.Log.Info("Running DB migrations...")
	schemaBytes, err := os.ReadFile("sql/schema.sql")
	if err != nil {
		logger.Log.Warn("sql/schema.sql not found, skipping migration", zap.Error(err))
	} else {
		if _, err := pool.Exec(context.Background(), string(schemaBytes)); err != nil {
			logger.Log.Fatal("DB migration failed", zap.Error(err))
		}
		logger.Log.Info("DB migration complete")
	}

	// 2. Repository
	repo := repository.NewPostgresRepository(pool)

	// 2.1 Redis
	redisCfg := redis.DefaultConfig()
	if redisAddr := os.Getenv("REDIS_URL"); redisAddr != "" {
		redisCfg.Addr = redisAddr
	}
	redisClient, err := redis.NewClient(redisCfg)
	if err != nil {
		logger.Log.Warn("Redis unavailable, falling back to memory mode", zap.Error(err))
	}
	var cacheRepo core.CacheRepository
	if redisClient != nil {
		cacheRepo = repository.NewRedisCacheRepository(redisClient)
		logger.Log.Info("Redis connected")
	}

	// 2.2 Kafka Producer (publish exchange.orders after TX1)
	kafkaCfg := kafka.DefaultConfig()
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		kafkaCfg.Brokers = strings.Split(brokers, ",")
	}
	if os.Getenv("KAFKA_ALLOW_AUTO_CREATE") == "false" {
		kafkaCfg.AllowAutoTopicCreation = false
	} else if os.Getenv("KAFKA_ALLOW_AUTO_CREATE") == "true" {
		kafkaCfg.AllowAutoTopicCreation = true
	} else if os.Getenv("GO_ENV") == "production" {
		kafkaCfg.AllowAutoTopicCreation = false
	}

	var eventBus core.EventPublisher
	var kafkaProducer *kafka.Producer
	if producer, perr := kafka.NewProducer(kafkaCfg); perr != nil {
		logger.Log.Warn("Kafka unavailable, falling back to sync matching mode", zap.Error(perr))
	} else {
		kafkaProducer = producer
		eventBus = producer
		logger.Log.Info("Kafka producer connected")
	}

	// 3. ExchangeService
	//    tradeListener = nil: WebSocket 已拆至 market-data-service
	//    eventBus = kafkaProducer: PlaceOrder / OrderUpdated publish to Kafka (async path)
	svc := core.NewExchangeService(
		repo, repo, repo, repo, repo,
		"BTC-USD",
		nil,
		cacheRepo,
		eventBus,
	)

	// Only restore engine snapshot in fallback (no-Kafka) mode.
	// Normal microservice mode: PlaceOrder publishes to Kafka; matching-engine owns the in-memory engine.
	if eventBus == nil {
		logger.Log.Info("No Kafka - falling back to sync matching, restoring engine snapshot...")
		if err := svc.RestoreEngineSnapshot(context.Background()); err != nil {
			logger.Log.Error("RestoreEngineSnapshot failed", zap.Error(err))
		}
	}

	// 5. Start Kafka consumers
	consumerCtx, cancelConsumers := context.WithCancel(context.Background())
	var settleConsumer *kafka.Consumer

	if eventBus != nil {
		// 5.1 Settlement consumer (exchange.settlements -> TX2 DB writes)
		var serr error
		settleConsumer, serr = kafka.NewConsumer(kafkaCfg, "settlement-engine", []string{core.TopicSettlements})
		if serr != nil {
			logger.Log.Error("Failed to create settlement consumer", zap.Error(serr))
		} else {
			settleConsumer.Start(consumerCtx, svc.HandleSettlementEvent)
			logger.Log.Info("Settlement consumer started", zap.String("topic", core.TopicSettlements))
		}
	}

	// 6. Simulator
	sim := simulator.NewService(svc)

	// 7. HTTP routes
	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173", "http://localhost:3000"},
		AllowMethods:     []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Idempotency-Key"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))

	handler := api.NewHandler(svc, sim)
	v1 := r.Group("/api/v1")
	handler.RegisterRoutes(v1)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "order-service"})
	})
	r.Static("/docs", "docs")
	url := ginSwagger.URL("http://localhost:8080/docs/swagger.yaml")
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, url))

	// 8. Start HTTP server
	port := os.Getenv("ORDER_SERVICE_PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	go func() {
		logger.Info("order-service started", zap.String("port", ":"+port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("listen", zap.Error(err))
		}
	}()

	// 9. Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("order-service shutdown signal received")

	cancelConsumers()
	shutdownDone := make(chan struct{})
	go func() {
		if settleConsumer != nil {
			settleConsumer.Wait()
		}
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		logger.Info("Kafka consumers closed")
	case <-time.After(10 * time.Second):
		logger.Warn("Kafka consumer close timeout, forcing shutdown")
	}

	if kafkaProducer != nil {
		kafkaProducer.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("order-service forced shutdown", zap.Error(err))
	}
	logger.Info("order-service shutdown complete")
}
