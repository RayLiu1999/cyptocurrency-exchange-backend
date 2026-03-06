package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// JoinArena 註冊匿名用戶
func (h *Handler) JoinArena(c *gin.Context) {
	user, accounts, err := h.svc.RegisterAnonymousUser(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	balances := make(map[string]decimal.Decimal)
	for _, acc := range accounts {
		balances[acc.Currency] = acc.Balance
	}

	c.JSON(http.StatusCreated, gin.H{
		"user_id":  user.ID,
		"email":    user.Email,
		"balances": balances,
	})
}

// GetBalances 取得用戶餘額
func (h *Handler) GetBalances(c *gin.Context) {
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

	accounts, err := h.svc.GetBalances(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, accounts)
}
