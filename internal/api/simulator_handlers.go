package api

import (
	"io"
	"net/http"

	"github.com/RayLiu1999/exchange/internal/simulator"
	"github.com/gin-gonic/gin"
)

type startSimulationRequest struct {
	Symbol      string  `json:"symbol"`
	BasePrice   float64 `json:"base_price"`
	NumTraders  int     `json:"num_traders"`
	TotalTx     int     `json:"total_tx"`
	WorkerCount int     `json:"worker_count"`
	Infinite    bool    `json:"infinite"`
	IntervalMs  int     `json:"interval_ms"`
}

func (h *Handler) StartSimulation(c *gin.Context) {
	if h.simulator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "模擬器尚未啟用"})
		return
	}

	var req startSimulationRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg := simulator.Config{
		Symbol:      req.Symbol,
		BasePrice:   req.BasePrice,
		NumTraders:  req.NumTraders,
		TotalTx:     req.TotalTx,
		WorkerCount: req.WorkerCount,
		Infinite:    req.Infinite,
		IntervalMs:  req.IntervalMs,
	}

	if err := h.simulator.Start(cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "模擬已啟動"})
}

func (h *Handler) StopSimulation(c *gin.Context) {
	if h.simulator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "模擬器尚未啟用"})
		return
	}

	if err := h.simulator.Stop(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "模擬已停止"})
}

func (h *Handler) GetSimulationStatus(c *gin.Context) {
	if h.simulator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "模擬器尚未啟用"})
		return
	}

	c.JSON(http.StatusOK, h.simulator.GetStatus())
}

// ClearSimulationData 清除交易資料
func (h *Handler) ClearSimulationData(c *gin.Context) {
	if err := h.svc.ClearSimulationData(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "交易資料已清除"})
}
