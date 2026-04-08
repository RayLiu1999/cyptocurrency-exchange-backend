package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	infraredis "github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	"github.com/RayLiu1999/exchange/internal/middleware"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func main() {
	defer logger.Sync()

	port := os.Getenv("GATEWAY_PORT")
	if port == "" {
		port = "8100"
	}

	orderServiceURL := os.Getenv("ORDER_SERVICE_URL")
	if orderServiceURL == "" {
		orderServiceURL = "http://localhost:8103"
	}
	marketDataURL := os.Getenv("MARKET_DATA_SERVICE_URL")
	if marketDataURL == "" {
		marketDataURL = "http://localhost:8102"
	}
	simulationServiceURL := os.Getenv("SIMULATION_SERVICE_URL")
	if simulationServiceURL == "" {
		simulationServiceURL = "http://localhost:8104"
	}

	orderTargetURL, err := url.Parse(orderServiceURL)
	if err != nil {
		logger.Log.Fatal("gateway: ORDER_SERVICE_URL 格式錯誤", zap.String("url", orderServiceURL), zap.Error(err))
	}
	marketTargetURL, err := url.Parse(marketDataURL)
	if err != nil {
		logger.Log.Fatal("gateway: MARKET_DATA_SERVICE_URL 格式錯誤", zap.String("url", marketDataURL), zap.Error(err))
	}
	simulationTargetURL, err := url.Parse(simulationServiceURL)
	if err != nil {
		logger.Log.Fatal("gateway: SIMULATION_SERVICE_URL 格式錯誤", zap.String("url", simulationServiceURL), zap.Error(err))
	}

	redisCfg := infraredis.DefaultConfig()
	if redisAddr := os.Getenv("REDIS_URL"); redisAddr != "" {
		redisCfg.Addr = redisAddr
	}
	redisClient, redisErr := infraredis.NewClient(redisCfg)
	if redisErr != nil {
		logger.Log.Warn("gateway: Redis 不可用，安全 middleware 退回記憶體模式", zap.Error(redisErr))
	}

	publicRateLimit := 200
	if val := os.Getenv("RATE_LIMIT_PUBLIC"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			publicRateLimit = parsed
		}
	}

	privateRateLimit := 100
	if val := os.Getenv("RATE_LIMIT_PRIVATE"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			privateRateLimit = parsed
		}
	}

	var publicLimiter middleware.RateLimiter
	var privateLimiter middleware.RateLimiter
	var idempStore middleware.IdempotencyStore
	if redisClient != nil {
		publicLimiter = middleware.NewRedisRateLimiter(redisClient, publicRateLimit, time.Second)
		privateLimiter = middleware.NewRedisRateLimiter(redisClient, privateRateLimit, time.Second)
		idempStore = middleware.NewRedisIdempotencyStore(redisClient)
	} else {
		publicLimiter = middleware.NewMemoryRateLimiter(5, publicRateLimit, 10*time.Minute)
		privateLimiter = middleware.NewMemoryRateLimiter(100, privateRateLimit, 10*time.Minute)
		idempStore = middleware.NewMemoryIdempotencyStore()
	}

	orderProxy := newReverseProxy(orderTargetURL)
	marketProxy := newReverseProxy(marketTargetURL)
	simulationProxy := newReverseProxy(simulationTargetURL)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(metrics.Middleware("gateway"))

	// Gateway 身為唯一入口，必須設定統一的 CORS 原則
	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOriginsFromEnv(),
		AllowMethods:     []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Idempotency-Key", "X-User-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	r.GET("/metrics", gin.WrapH(metrics.Handler()))
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":              "ok",
			"service":             "gateway",
			"order_service":       orderTargetURL.String(),
			"market_data_service": marketTargetURL.String(),
		})
	})
	r.Any("/ws", gin.WrapH(marketProxy))
	r.Any("/docs/*path", gin.WrapH(orderProxy))
	r.Any("/swagger/*path", gin.WrapH(orderProxy))

	apiGroup := r.Group("/api/v1")
	{
		public := apiGroup.Group("/")
		public.Use(middleware.RateLimitMiddleware(publicLimiter))
		public.GET("/orderbook", gin.WrapH(marketProxy))
		public.GET("/klines", gin.WrapH(marketProxy))
		public.GET("/trades", gin.WrapH(marketProxy))

		private := apiGroup.Group("/")
		private.Use(middleware.RateLimitMiddleware(privateLimiter))
		private.GET("/orders", gin.WrapH(orderProxy))
		private.GET("/orders/:id", gin.WrapH(orderProxy))
		private.DELETE("/orders/:id", gin.WrapH(orderProxy))
		private.GET("/accounts", gin.WrapH(orderProxy))
		private.POST("/test/join", gin.WrapH(orderProxy))
		private.POST("/test/recharge/:user_id", gin.WrapH(orderProxy))

		orders := apiGroup.Group("/")
		orders.Use(middleware.RateLimitMiddleware(privateLimiter))
		orders.Use(middleware.IdempotencyMiddleware(idempStore, 24*time.Hour))
		orders.POST("/orders", gin.WrapH(orderProxy))
		orders.POST("/orders/batch", gin.WrapH(orderProxy))

		// 模擬器控制 API（轉發至 simulation-service，無需冪等性保護）
		simulation := apiGroup.Group("/")
		simulation.POST("/simulation/start", gin.WrapH(simulationProxy))
		simulation.POST("/simulation/stop", gin.WrapH(simulationProxy))
		simulation.GET("/simulation/status", gin.WrapH(simulationProxy))
	}
	r.NoRoute(gin.WrapH(orderProxy))

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%s", port),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Log.Info("gateway 啟動完成",
			zap.String("port", port),
			zap.String("order_upstream", orderTargetURL.String()),
			zap.String("market_upstream", marketTargetURL.String()),
			zap.String("simulation_upstream", simulationTargetURL.String()),
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Fatal("gateway 啟動失敗", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Log.Info("gateway 收到關閉訊號", zap.String("signal", sig.String()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Log.Error("gateway 關閉失敗", zap.Error(err))
		return
	}
	logger.Log.Info("gateway 已完成關閉")
}

func allowedOriginsFromEnv() []string {
	defaultOrigins := []string{"http://localhost:5173", "http://localhost:3000"}
	rawOrigins := strings.TrimSpace(os.Getenv("ORIGIN_URL"))
	if rawOrigins == "" {
		return defaultOrigins
	}

	parts := strings.Split(rawOrigins, ",")
	origins := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		origin := strings.TrimSpace(part)
		if origin == "" {
			continue
		}
		if _, exists := seen[origin]; exists {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}

	if len(origins) == 0 {
		return defaultOrigins
	}
	return origins
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logger.Log.Error("gateway: JSON 回應寫入失敗", zap.Error(err))
	}
}

func newReverseProxy(targetURL *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Header.Set("X-Forwarded-Host", req.Host)
		if req.TLS != nil {
			req.Header.Set("X-Forwarded-Proto", "https")
			return
		}
		req.Header.Set("X-Forwarded-Proto", "http")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Log.Error("gateway: 反向代理失敗", zap.String("path", r.URL.Path), zap.Error(err))
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":   "upstream unavailable",
			"service": "gateway",
		})
	}
	return proxy
}
