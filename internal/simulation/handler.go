package simulation

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler 提供模擬器的 HTTP 控制介面
type Handler struct {
	svc *Service
}

// NewHandler 建立 HTTP Handler
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes 註冊路由至 router group
func (h *Handler) RegisterRoutes(rg gin.IRouter) {
	rg.POST("/simulation/start", h.Start)
	rg.POST("/simulation/stop", h.Stop)
	rg.GET("/simulation/status", h.GetStatus)
}

// Start godoc
// @Summary 啟動模擬交易機器人
// @Description 啟動指定數量的機器人，透過 Gateway 發送模擬訂單
// @Tags simulation
// @Accept json
// @Produce json
// @Param config body Config true "模擬器配置"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /simulation/start [post]
func (h *Handler) Start(c *gin.Context) {
	var cfg Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		// 允許不帶 body（使用預設值）
		cfg = Config{}
	}

	if err := h.svc.Start(cfg); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "模擬器已啟動",
		"symbol":  cfg.Symbol,
	})
}

// Stop godoc
// @Summary 停止模擬交易機器人
func (h *Handler) Stop(c *gin.Context) {
	if err := h.svc.Stop(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "模擬器已停止"})
}

// GetStatus godoc
// @Summary 取得模擬器目前狀態
func (h *Handler) GetStatus(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.GetStatus())
}
