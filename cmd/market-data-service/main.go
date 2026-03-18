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
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func main() {
	defer logger.Sync()

	kafkaCfg := kafka.DefaultConfig()
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		kafkaCfg.Brokers = strings.Split(brokers, ",")
	}

	wsHandler := api.NewWebSocketHandler()
	go wsHandler.Run()

	svc := core.NewExchangeService(nil, nil, nil, nil, nil, "BTC-USD", wsHandler, nil, nil)

	consumerCtx, cancelConsumers := context.WithCancel(context.Background())
	var orderBookConsumer *kafka.Consumer
	var tradeConsumer *kafka.Consumer
	var orderUpdateConsumer *kafka.Consumer

	var err error
	orderBookConsumer, err = kafka.NewConsumer(kafkaCfg, "market-data-orderbook", []string{core.TopicOrderBook})
	if err != nil {
		logger.Log.Fatal("market-data-service: 無法建立 orderbook consumer", zap.Error(err))
	}
	orderBookConsumer.Start(consumerCtx, svc.HandleOrderBookEvent)

	tradeConsumer, err = kafka.NewConsumer(kafkaCfg, "market-data-trades", []string{core.TopicTrades})
	if err != nil {
		logger.Log.Fatal("market-data-service: 無法建立 trade consumer", zap.Error(err))
	}
	tradeConsumer.Start(consumerCtx, svc.HandleTradeEvent)

	orderUpdateConsumer, err = kafka.NewConsumer(kafkaCfg, "market-data-order-updates", []string{core.TopicOrderUpdates})
	if err != nil {
		logger.Log.Fatal("market-data-service: 無法建立 order update consumer", zap.Error(err))
	}
	orderUpdateConsumer.Start(consumerCtx, svc.HandleOrderUpdatedEvent)

	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173", "http://localhost:3000", "http://localhost:8084"},
		AllowMethods:     []string{"GET", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "market-data-service"})
	})
	r.GET("/ws", wsHandler.HandleWS)

	port := os.Getenv("MARKET_DATA_PORT")
	if port == "" {
		port = "8083"
	}
	srv := &http.Server{Addr: ":" + port, Handler: r}

	go func() {
		logger.Log.Info("market-data-service started", zap.String("port", port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Fatal("market-data-service 啟動失敗", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Log.Info("market-data-service shutdown signal received")

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
		logger.Log.Info("market-data Kafka consumers closed")
	case <-time.After(10 * time.Second):
		logger.Log.Warn("market-data consumer close timeout, forcing shutdown")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Log.Error("market-data-service forced shutdown", zap.Error(err))
	}
	logger.Log.Info("market-data-service shutdown complete")
}
