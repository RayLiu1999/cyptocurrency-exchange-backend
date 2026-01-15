package main

import (
	"context"
	"log"
	"os"

	"github.com/RayLiu1999/exchange/internal/api"
	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/RayLiu1999/exchange/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// 1. 資料庫連線
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://user:password@localhost:5432/exchange?sslmode=disable"
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		log.Fatalf("無法連接資料庫: %v\n", err)
	}
	defer pool.Close()

	// 2. Repository
	repo := repository.NewPostgresRepository(pool)

	// 3. Service (內建撮合引擎)
	svc := core.NewExchangeService(repo, repo, "BTC-USD")

	// 4. HTTP Handler
	handler := api.NewHandler(svc)

	// 5. 啟動伺服器
	r := gin.Default()
	handler.RegisterRoutes(r)

	log.Println("🚀 伺服器啟動於 :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("伺服器啟動失敗: %v", err)
	}
}
