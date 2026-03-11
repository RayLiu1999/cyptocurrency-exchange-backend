package api

import (
	"net/http"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type placeOrderRequest struct {
	UserID   string          `json:"user_id" binding:"required"`
	Symbol   string          `json:"symbol" binding:"required"`
	Side     string          `json:"side" binding:"required,oneof=BUY SELL"`
	Type     string          `json:"type" binding:"required,oneof=LIMIT MARKET"`
	Price    decimal.Decimal `json:"price"` // 市價單可不傳
	Quantity decimal.Decimal `json:"quantity" binding:"required"`
}

// PlaceOrder 下單
func (h *Handler) PlaceOrder(c *gin.Context) {
	var req placeOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 執行數值範圍與業務規則驗證（Symbol、Quantity、Price 精度與上下限）
	if err := validatePlaceOrderRequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	side, err := core.SideFromString(req.Side)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	orderType, err := core.TypeFromString(req.Type)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	order := &core.Order{
		UserID:   userID,
		Symbol:   req.Symbol,
		Side:     side,
		Type:     orderType,
		Price:    req.Price,
		Quantity: req.Quantity,
	}

	if err := h.svc.PlaceOrder(c.Request.Context(), order); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":              order.ID,
		"status":          core.StatusToString(order.Status),
		"filled_quantity": order.FilledQuantity,
	})
}

// GetOrder 取得單一訂單
func (h *Handler) GetOrder(c *gin.Context) {
	idStr := c.Param("id")
	orderID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無效的訂單 ID"})
		return
	}

	order, err := h.svc.GetOrder(c.Request.Context(), orderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "訂單不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":              order.ID,
		"user_id":         order.UserID,
		"symbol":          order.Symbol,
		"side":            core.SideToString(order.Side),
		"type":            core.TypeToString(order.Type),
		"price":           order.Price,
		"quantity":        order.Quantity,
		"filled_quantity": order.FilledQuantity,
		"status":          core.StatusToString(order.Status),
		"created_at":      order.CreatedAt,
	})
}

// GetOrders 取得用戶訂單列表
func (h *Handler) GetOrders(c *gin.Context) {
	userIDStr := c.Query("user_id")
	if userIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 user_id 參數"})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無效的 user_id"})
		return
	}

	orders, err := h.svc.GetOrdersByUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	result := make([]gin.H, len(orders))
	for i, order := range orders {
		result[i] = gin.H{
			"id":              order.ID,
			"symbol":          order.Symbol,
			"side":            core.SideToString(order.Side),
			"status":          core.StatusToString(order.Status),
			"price":           order.Price,
			"quantity":        order.Quantity,
			"filled_quantity": order.FilledQuantity,
			"created_at":      order.CreatedAt,
		}
	}

	c.JSON(http.StatusOK, result)
}

// CancelOrder 取消訂單
func (h *Handler) CancelOrder(c *gin.Context) {
	idStr := c.Param("id")
	orderID, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無效的訂單 ID"})
		return
	}

	// 從 Header 或 Query 取得 user_id (簡化版，之後會用 JWT)
	userIDStr := c.GetHeader("X-User-ID")
	if userIDStr == "" {
		userIDStr = c.Query("user_id")
	}
	if userIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 user_id"})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無效的 user_id"})
		return
	}

	if err := h.svc.CancelOrder(c.Request.Context(), orderID, userID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "訂單已取消"})
}
