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
	"github.com/RayLiu1999/exchange/internal/infrastructure/db"
	"github.com/RayLiu1999/exchange/internal/infrastructure/kafka"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	"github.com/RayLiu1999/exchange/internal/marketdata"
	"github.com/RayLiu1999/exchange/internal/repository"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// market-data-service: 行情推播服務
// 職責：消費 Kafka 行情事件，透過 WebSocket 即時推播至前端
// 消費 Kafka：exchange.orderbook, exchange.trades, exchange.order_updates
// 無資料庫依賴，無 DB 寫入，純事件轉發
func main() {
	defer logger.Sync()

	kafkaCfg := kafka.DefaultConfig()
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		kafkaCfg.Brokers = strings.Split(brokers, ",")
	}
	if resetOffset := os.Getenv("KAFKA_RESET_OFFSET"); resetOffset != "" {
		kafkaCfg.ResetOffset = strings.ToLower(resetOffset)
	}

	// Connect to Database for queries
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		logger.Log.Fatal("market-data-service: DATABASE_URL 環境變數未設定")
	}
	dbCfg := db.DefaultDBConfig(dbURL)
	dbCfg.MaxOpenConns = 50 // 行情服務的 DB 查詢為輔助性質（主要為快取），適中即可
	pool, err := db.NewPostgresPool(context.Background(), dbCfg)
	if err != nil {
		logger.Log.Fatal("market-data-service: 無法連接資料庫", zap.Error(err))
	}
	defer pool.Close()

	repo := repository.NewPostgresRepository(pool)

	redisCfg := redis.DefaultConfig()
	if redisAddr := os.Getenv("REDIS_URL"); redisAddr != "" {
		redisCfg.Addr = redisAddr
	}
	redisClient, redisErr := redis.NewClient(redisCfg)
	if redisErr != nil {
		logger.Log.Warn("Redis 不可用", zap.Error(redisErr))
	}
	var cacheRepo domain.CacheRepository
	if redisClient != nil {
		cacheRepo = repository.NewRedisCacheRepository(redisClient)
	}

	// MarketDataService
	wsHandler := api.NewWebSocketHandler("market-data-service")
	go wsHandler.Run()

	// MarketData Service
	svc := marketdata.NewSubscriber(wsHandler, cacheRepo)

	querySvc := marketdata.NewQueryService(repo, cacheRepo)

	// 8. 啟動 Kafka Consumers
	consumerCtx, cancelConsumers := context.WithCancel(context.Background())
	var orderBookConsumer *kafka.Consumer
	var tradeConsumer *kafka.Consumer
	var orderUpdateConsumer *kafka.Consumer

	orderBookConsumer, err = kafka.NewConsumer(kafkaCfg, "market-data-orderbook", []string{domain.TopicOrderBook})
	if err != nil {
		logger.Log.Fatal("market-data-service: 建立 orderbook consumer 失敗", zap.Error(err))
	}
	orderBookConsumer.Start(consumerCtx, svc.HandleOrderBook)
	logger.Log.Info("OrderBook consumer 已啟動", zap.String("topic", domain.TopicOrderBook))

	tradeConsumer, err = kafka.NewConsumer(kafkaCfg, "market-data-trades", []string{domain.TopicTrades})
	if err != nil {
		logger.Log.Fatal("market-data-service: 建立 trade consumer 失敗", zap.Error(err))
	}
	tradeConsumer.Start(consumerCtx, svc.HandleTrade)
	logger.Log.Info("Trade consumer 已啟動", zap.String("topic", domain.TopicTrades))

	orderUpdateConsumer, err = kafka.NewConsumer(kafkaCfg, "market-data-order-updates", []string{domain.TopicOrderUpdates})
	if err != nil {
		logger.Log.Fatal("market-data-service: 建立 order-update consumer 失敗", zap.Error(err))
	}
	orderUpdateConsumer.Start(consumerCtx, svc.HandleOrderUpdated)

	// HTTP 伺服器
	r := gin.Default()
	r.Use(metrics.Middleware("market-data-service"))
	r.GET("/metrics", gin.WrapH(metrics.Handler()))
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "market-data-service"})
	})
	r.GET("/ws", wsHandler.HandleWS)

	handler := api.NewHandler(nil, querySvc)
	v1 := r.Group("/api/v1")
	// market-data-service 負責公開查詢
	v1.GET("/orderbook", handler.GetOrderBook)
	v1.GET("/klines", handler.GetKLines)
	v1.GET("/trades", handler.GetRecentTrades)

	port := os.Getenv("MARKET_DATA_PORT")
	if port == "" {
		port = "8102"
	}
	srv := &http.Server{Addr: ":" + port, Handler: r}

	go func() {
		logger.Log.Info("market-data-service 已啟動", zap.String("port", port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Fatal("market-data-service 啟動失敗", zap.Error(err))
		}
	}()

	// 優雅關機
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Log.Info("market-data-service 收到關閉訊號")

	cancelConsumers()
	shutdownDone := make(chan struct{})
	go func() {
		if orderBookConsumer != nil {
			orderBookConsumer.Wait()
		}
		if tradeConsumer != nil {
			tradeConsumer.Wait()
		}
		if orderUpdateConsumer != nil {
			orderUpdateConsumer.Wait()
		}
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		logger.Log.Info("market-data Kafka consumers 已關閉")
	case <-time.After(10 * time.Second):
		logger.Log.Warn("market-data consumer 等待超時，強制繼續關機")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Log.Error("market-data-service 強制關閉", zap.Error(err))
	}
	logger.Log.Info("market-data-service 優雅關機完成")
}
