package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RayLiu1999/exchange/internal/api"
	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
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

	// 2. Repository
	repo := repository.NewPostgresRepository(pool)

	// 3. WebSocket Handler (先建立，作為事件監聽者)
	wsHandler := api.NewWebSocketHandler()
	go wsHandler.Run()
	// wsHandler.StartBroadcastingDummyData() // 已移除，改用 Real Data

	// 4. Service (內建撮合引擎，注入 repo 作為所有的 Repository 實現)
	svc := core.NewExchangeService(repo, repo, repo, repo, repo, "BTC-USD", wsHandler)

	// 啟動時從資料庫還原未完成的訂單，重建掛單簿
	if err := svc.RestoreEngineSnapshot(context.Background()); err != nil {
		logger.Log.Error("還原撮合引擎快照失敗", zap.Error(err))
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

	// 初始化限流器（Token Bucket）與冪等性 Store（Memory）
	publicLimiter := middleware.NewMemoryRateLimiter(1, 60, 10*time.Minute)   // 公共 API：60 次/分鐘/IP
	privateLimiter := middleware.NewMemoryRateLimiter(10, 10, 10*time.Minute) // 私有 API：10 次/秒/IP
	idempStore := middleware.NewMemoryIdempotencyStore()

	// API v1 Routing Group，掛載 5. HTTP Handler
	handler := api.NewHandler(svc, sim)
	v1 := r.Group("/api/v1")
	handler.RegisterRoutes(v1, publicLimiter, privateLimiter, idempStore)

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

	// 設定超時時間，等待當前請求處理完畢
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("伺服器強制關閉", zap.Error(err))
	}

	logger.Info("伺服器已優雅關閉")
}
