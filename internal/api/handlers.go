package api

import (
	"time"

	"github.com/RayLiu1999/exchange/internal/marketdata"
	"github.com/RayLiu1999/exchange/internal/middleware"
	"github.com/RayLiu1999/exchange/internal/order"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	orderSvc order.OrderService
	querySvc marketdata.QueryService // QueryService 負責 public reads
}

func NewHandler(orderSvc order.OrderService, querySvc marketdata.QueryService) *Handler {
	return &Handler{
		orderSvc: orderSvc,
		querySvc: querySvc,
	}
}

// RegisterRoutes 註冊純業務路由，不在 order-service 內綁定安全 Middleware。
// Gateway 已在邊界層完成限流與冪等性保護，order-service 僅負責業務邏輯。
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
		private.POST("/test/join", h.JoinArena)
		private.POST("/test/recharge/:user_id", h.RechargeTestUser)
	}

	orders := router.Group("/")
	{
		orders.POST("/orders", h.PlaceOrder)
	}
}

// RegisterRoutesWithMiddleware 供測試使用，掛載安全 Middleware。
// 生產環境下安全 Middleware 應在 Gateway 層完成，不應在 order-service 重複掛載。
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
