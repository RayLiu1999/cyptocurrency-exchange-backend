package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

/*
=== TDD TODO List: API Layer ===

Step 4: API 整合測試
- [x] 4.1 POST /orders 參數錯誤應返回 400
- [x] 4.2 POST /orders 成功應返回 201

Step 5: 訂單查詢 API
- [x] 5.1 GET /orders/:id 應返回訂單詳情
- [x] 5.2 GET /orders?user_id=xxx 應返回用戶訂單列表

=====================================
*/

// ============================================================
// Mock Service
// ============================================================

type MockExchangeService struct {
	mock.Mock
}

func (m *MockExchangeService) PlaceOrder(ctx context.Context, order *core.Order) error {
	args := m.Called(ctx, order)
	if args.Error(0) == nil {
		order.ID = uuid.New()
		order.Status = core.StatusNew
	}
	return args.Error(0)
}

func (m *MockExchangeService) GetOrder(ctx context.Context, id uuid.UUID) (*core.Order, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*core.Order), args.Error(1)
}

func (m *MockExchangeService) GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*core.Order, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*core.Order), args.Error(1)
}

func setupRouter(svc core.ExchangeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handler := NewHandler(svc)
	handler.RegisterRoutes(r)
	return r
}

// ============================================================
// Step 4: API 整合測試
// ============================================================

// TODO 4.1: POST /orders 參數錯誤應返回 400
func TestPlaceOrderAPI_InvalidParams_Returns400(t *testing.T) {
	// Arrange
	mockSvc := &MockExchangeService{}
	router := setupRouter(mockSvc)

	// 缺少必要欄位
	body := `{"symbol": "BTC-USD"}`

	// Act
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/orders", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TODO 4.2: POST /orders 成功應返回 201
func TestPlaceOrderAPI_Success_Returns201(t *testing.T) {
	// Arrange
	mockSvc := &MockExchangeService{}
	mockSvc.On("PlaceOrder", mock.Anything, mock.Anything).Return(nil)

	router := setupRouter(mockSvc)

	requestBody := map[string]interface{}{
		"user_id":  uuid.New().String(),
		"symbol":   "BTC-USD",
		"side":     "BUY",
		"type":     "LIMIT",
		"price":    "50000",
		"quantity": "1",
	}
	body, _ := json.Marshal(requestBody)

	// Act
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/orders", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusCreated, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.NotEmpty(t, response["id"])
	assert.Equal(t, "NEW", response["status"])
}

// ============================================================
// Step 5: 訂單查詢 API
// ============================================================

// TODO 5.1: GET /orders/:id 應返回訂單詳情
func TestGetOrderAPI_Success_ReturnsOrder(t *testing.T) {
	// Arrange
	mockSvc := &MockExchangeService{}
	orderID := uuid.New()
	expectedOrder := &core.Order{
		ID:             orderID,
		UserID:         uuid.New(),
		Symbol:         "BTC-USD",
		Side:           core.SideBuy,
		Price:          decimal.NewFromInt(50000),
		Quantity:       decimal.NewFromInt(1),
		FilledQuantity: decimal.Zero,
		Status:         core.StatusNew,
	}
	mockSvc.On("GetOrder", mock.Anything, orderID).Return(expectedOrder, nil)

	router := setupRouter(mockSvc)

	// Act
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/orders/"+orderID.String(), nil)
	router.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, orderID.String(), response["id"])
}

// TODO 5.2: GET /orders?user_id=xxx 應返回用戶訂單列表
func TestGetOrdersAPI_ByUserID_ReturnsOrders(t *testing.T) {
	// Arrange
	mockSvc := &MockExchangeService{}
	userID := uuid.New()
	orders := []*core.Order{
		{ID: uuid.New(), UserID: userID, Symbol: "BTC-USD", Status: core.StatusNew},
		{ID: uuid.New(), UserID: userID, Symbol: "BTC-USD", Status: core.StatusFilled},
	}
	mockSvc.On("GetOrdersByUser", mock.Anything, userID).Return(orders, nil)

	router := setupRouter(mockSvc)

	// Act
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/orders?user_id="+userID.String(), nil)
	router.ServeHTTP(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Len(t, response, 2)
}
