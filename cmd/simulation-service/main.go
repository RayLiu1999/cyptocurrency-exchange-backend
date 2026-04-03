package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"github.com/RayLiu1999/exchange/internal/simulation"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// simulation-service：模擬交易壓測微服務
// 透過 HTTP API 對 Gateway 發送模擬訂單，完全不依賴任何 internal 業務程式碼
// 提供 Start/Stop/Status API 供前端控制
func main() {
	defer logger.Sync()

	// 1. 讀取配置
	gatewayURL := os.Getenv("GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://localhost:8100"
	}

	port := os.Getenv("SIMULATION_SERVICE_PORT")
	if port == "" {
		port = "8104"
	}

	logger.Log.Info("simulation-service 初始化",
		zap.String("gateway_url", gatewayURL),
		zap.String("port", port),
	)

	// 2. 建立核心服務與 Handler
	svc := simulation.NewService(gatewayURL)
	h := simulation.NewHandler(svc)

	// 3. 路由設定
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(metrics.Middleware("simulation-service"))

	r.GET("/metrics", gin.WrapH(metrics.Handler()))
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":      "ok",
			"service":     "simulation-service",
			"gateway_url": gatewayURL,
		})
	})

	// 模擬控制 API（供 Gateway 轉發）
	api := r.Group("/api/v1")
	h.RegisterRoutes(api)

	// 4. 啟動 HTTP 伺服器
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%s", port),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Log.Info("simulation-service 已啟動", zap.String("port", port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Fatal("simulation-service 啟動失敗", zap.Error(err))
		}
	}()

	// 5. 優雅關機
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Log.Info("simulation-service 收到關閉訊號", zap.String("signal", sig.String()))

	// 停止機器人
	_ = svc.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Log.Error("simulation-service 關閉失敗", zap.Error(err))
	}
	logger.Log.Info("simulation-service 已完成關閉")
}
