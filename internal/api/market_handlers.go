package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// GetOrderBook 取得訂單簿
func (h *Handler) GetOrderBook(c *gin.Context) {
	symbol := c.DefaultQuery("symbol", "BTC-USD")

	orderBook, err := h.querySvc.GetOrderBook(c.Request.Context(), symbol)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, orderBook)
}

// GetKLines 取得 K 線
func (h *Handler) GetKLines(c *gin.Context) {
	symbol := c.DefaultQuery("symbol", "BTC-USD")
	interval := c.DefaultQuery("interval", "1m")
	limitStr := c.DefaultQuery("limit", "100")

	limit := 100
	fmt.Sscanf(limitStr, "%d", &limit)

	klines, err := h.querySvc.GetKLines(c.Request.Context(), symbol, interval, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, klines)
}

// GetRecentTrades 取得最近成交
func (h *Handler) GetRecentTrades(c *gin.Context) {
	symbol := c.DefaultQuery("symbol", "BTC-USD")
	limitStr := c.DefaultQuery("limit", "50")

	limit := 50
	fmt.Sscanf(limitStr, "%d", &limit)

	trades, err := h.querySvc.GetRecentTrades(c.Request.Context(), symbol, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, trades)
}
