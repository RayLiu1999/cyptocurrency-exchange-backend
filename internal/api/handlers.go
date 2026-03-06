package api

import (
	"github.com/RayLiu1999/exchange/internal/core"
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

// RegisterRoutes 註冊路由
func (h *Handler) RegisterRoutes(router gin.IRouter) {
	router.GET("/orders", h.GetOrders)
	router.POST("/orders", h.PlaceOrder)
	router.GET("/orders/:id", h.GetOrder)
	router.DELETE("/orders/:id", h.CancelOrder)
	router.GET("/orderbook", h.GetOrderBook)
	router.POST("/test/join", h.JoinArena)
	router.GET("/accounts", h.GetBalances)
	router.GET("/klines", h.GetKLines)
	router.GET("/trades", h.GetRecentTrades)
	router.POST("/simulation/start", h.StartSimulation)
	router.POST("/simulation/stop", h.StopSimulation)
	router.GET("/simulation/status", h.GetSimulationStatus)
	router.DELETE("/simulation/data", h.ClearSimulationData)
}
