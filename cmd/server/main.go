package main

import (
	"context"
	"os"

	"github.com/RayLiu1999/exchange/internal/api"
	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
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

	// 3. Service (內建撮合引擎)
	svc := core.NewExchangeService(repo, repo, repo, repo, "BTC-USD")

	// 4. HTTP Handler
	handler := api.NewHandler(svc)

	// 5. 啟動伺服器
	r := gin.Default()
	handler.RegisterRoutes(r)

	logger.Info("🚀 伺服器啟動", zap.String("port", ":8080"))
	if err := r.Run(":8080"); err != nil {
		logger.Log.Fatal("伺服器啟動失敗", zap.Error(err))
	}
}
