package api

import (
	"time"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/RayLiu1999/exchange/internal/middleware"
	"github.com/RayLiu1999/exchange/internal/simulator"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc       core.ExchangeService
	simulator *simulator.Service
}

func NewHandler(svc core.ExchangeService, sim *simulator.Service) *Handler {
	return &Handler{svc: svc, simulator: sim}
}

// RegisterRoutes 註冊純業務路由，不在 order-service 內綁定安全 Middleware。
// Gateway 版本可改呼叫 RegisterRoutesWithMiddleware 以前移限流與冪等性。
func (h *Handler) RegisterRoutes(router gin.IRouter) {
	public := router.Group("/")
	{
		public.GET("/orderbook", h.GetOrderBook)
		public.GET("/klines", h.GetKLines)
		public.GET("/trades", h.GetRecentTrades)
	}

	private := router.Group("/")
	{
		private.GET("/orders", h.GetOrders)
		private.GET("/orders/:id", h.GetOrder)
		private.DELETE("/orders/:id", h.CancelOrder)
		private.GET("/accounts", h.GetBalances)
		private.POST("/simulation/start", h.StartSimulation)
		private.POST("/simulation/stop", h.StopSimulation)
		private.GET("/simulation/status", h.GetSimulationStatus)
		private.DELETE("/simulation/data", h.ClearSimulationData)
		private.POST("/test/join", h.JoinArena)
	}

	orders := router.Group("/")
	{
		orders.POST("/orders", h.PlaceOrder)
	}
}

// RegisterRoutesWithMiddleware 註冊路由並依路由類型掛載對應的安全 Middleware。
// 主要供 Gateway 使用，將限流與冪等性前移到邊界層。
func (h *Handler) RegisterRoutesWithMiddleware(router gin.IRouter, publicLimiter, privateLimiter middleware.RateLimiter, idempStore middleware.IdempotencyStore) {
	// === 公共 API（讀取）：掛載寬鬆限流 ===
	public := router.Group("/")
	public.Use(middleware.RateLimitMiddleware(publicLimiter))
	{
		public.GET("/orderbook", h.GetOrderBook)
		public.GET("/klines", h.GetKLines)
		public.GET("/trades", h.GetRecentTrades)
	}

	// === 私有 API（寫入/查詢）：掛載嚴格限流 ===
	private := router.Group("/")
	private.Use(middleware.RateLimitMiddleware(privateLimiter))
	{
		private.GET("/orders", h.GetOrders)
		private.GET("/orders/:id", h.GetOrder)
		private.DELETE("/orders/:id", h.CancelOrder)
		private.GET("/accounts", h.GetBalances)

		// 模擬器管理 API
		private.POST("/simulation/start", h.StartSimulation)
		private.POST("/simulation/stop", h.StopSimulation)
		private.GET("/simulation/status", h.GetSimulationStatus)
		private.DELETE("/simulation/data", h.ClearSimulationData)

		// 測試工具
		private.POST("/test/join", h.JoinArena)
	}

	// === 下單 API：最嚴格限流 + 冪等性保護 ===
	orders := router.Group("/")
	orders.Use(middleware.RateLimitMiddleware(privateLimiter))
	orders.Use(middleware.IdempotencyMiddleware(idempStore, 24*time.Hour))
	{
		orders.POST("/orders", h.PlaceOrder)
	}
}
