package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

/*
=== TDD TODO List: API Layer ===

Phase 4: API 整合測試 ✅ DONE
- [x] 4.1 POST /orders 參數錯誤應返回 400
- [x] 4.2 POST /orders 成功應返回 201

Phase 5: 訂單查詢 API ✅ DONE
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

func (m *MockExchangeService) CancelOrder(ctx context.Context, orderID, userID uuid.UUID) error {
	args := m.Called(ctx, orderID, userID)
	return args.Error(0)
}

func (m *MockExchangeService) GetOrderBook(ctx context.Context, symbol string) (*matching.OrderBookSnapshot, error) {
	args := m.Called(ctx, symbol)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*matching.OrderBookSnapshot), args.Error(1)
}

func (m *MockExchangeService) RegisterAnonymousUser(ctx context.Context) (*core.User, []*core.Account, error) {
	args := m.Called(ctx)
	user, _ := args.Get(0).(*core.User)
	accounts, _ := args.Get(1).([]*core.Account)
	return user, accounts, args.Error(2)
}

func (m *MockExchangeService) GetBalances(ctx context.Context, userID uuid.UUID) ([]*core.Account, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*core.Account), args.Error(1)
}

func (m *MockExchangeService) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*core.KLine, error) {
	args := m.Called(ctx, symbol, interval, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*core.KLine), args.Error(1)
}

func (m *MockExchangeService) GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*matching.Trade, error) {
	args := m.Called(ctx, symbol, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*matching.Trade), args.Error(1)
}

func (m *MockExchangeService) ClearSimulationData(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockExchangeService) RechargeTestUser(ctx context.Context, userID uuid.UUID) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockExchangeService) RestoreEngineSnapshot(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func setupRouter(svc core.ExchangeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handler := NewHandler(svc, nil)
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

// ============================================================
// Phase 6: 取消訂單 API
// ============================================================

// 6.1 合法 order_id 與 user_id 透過 Header 可成功取消
func TestCancelOrderAPI_Success_Returns200(t *testing.T) {
	mockSvc := &MockExchangeService{}
	orderID := uuid.New()
	userID := uuid.New()
	mockSvc.On("CancelOrder", mock.Anything, orderID, userID).Return(nil)

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/orders/"+orderID.String()+"?user_id="+userID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockSvc.AssertExpectations(t)
}

// 6.2 缺少 user_id 時應返回 400
func TestCancelOrderAPI_MissingUserID_Returns400(t *testing.T) {
	mockSvc := &MockExchangeService{}
	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/orders/"+uuid.New().String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 6.3 order_id 不是合法 UUID 應返回 400
func TestCancelOrderAPI_InvalidOrderID_Returns400(t *testing.T) {
	mockSvc := &MockExchangeService{}
	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/orders/not-a-uuid?user_id="+uuid.New().String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 6.4 user_id 不是合法 UUID 應返回 400
func TestCancelOrderAPI_InvalidUserID_Returns400(t *testing.T) {
	mockSvc := &MockExchangeService{}
	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/orders/"+uuid.New().String()+"?user_id=not-a-uuid", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 6.5 Service 回傳業務錯誤時應返回 400
func TestCancelOrderAPI_ServiceError_Returns400(t *testing.T) {
	mockSvc := &MockExchangeService{}
	orderID := uuid.New()
	userID := uuid.New()
	mockSvc.On("CancelOrder", mock.Anything, orderID, userID).Return(fmt.Errorf("訂單已成交，不可取消"))

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/orders/"+orderID.String()+"?user_id="+userID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSvc.AssertExpectations(t)
}

// ============================================================
// Phase 7: 訂單簿、K 線、成交記錄 API
// ============================================================

// 7.1 GET /orderbook?symbol=... 正常回傳快照
func TestGetOrderBookAPI_Success_ReturnsSnapshot(t *testing.T) {
	mockSvc := &MockExchangeService{}
	snapshot := &matching.OrderBookSnapshot{
		Symbol: "BTC-USD",
		Bids:   []matching.OrderBookLevel{},
		Asks:   []matching.OrderBookLevel{},
	}
	mockSvc.On("GetOrderBook", mock.Anything, "BTC-USD").Return(snapshot, nil)

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/orderbook?symbol=BTC-USD", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "BTC-USD", resp["symbol"])
	mockSvc.AssertExpectations(t)
}

// 7.2 GetOrderBook Service 失敗應返回 500
func TestGetOrderBookAPI_ServiceError_Returns500(t *testing.T) {
	mockSvc := &MockExchangeService{}
	mockSvc.On("GetOrderBook", mock.Anything, mock.AnythingOfType("string")).Return(nil, fmt.Errorf("內部錯誤"))

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/orderbook?symbol=BTC-USD", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// 7.3 GET /klines 正常回傳資料
func TestGetKLinesAPI_Success_ReturnsData(t *testing.T) {
	mockSvc := &MockExchangeService{}
	klines := []*core.KLine{
		{Timestamp: 1700000000000, Open: decimal.NewFromInt(50000), High: decimal.NewFromInt(51000), Low: decimal.NewFromInt(49000), Close: decimal.NewFromInt(50500)},
	}
	mockSvc.On("GetKLines", mock.Anything, "BTC-USD", "1m", 100).Return(klines, nil)

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/klines?symbol=BTC-USD&interval=1m", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Len(t, resp, 1)
}

// 7.4 GET /trades 正常回傳成交記錄
func TestGetRecentTradesAPI_Success_ReturnsData(t *testing.T) {
	mockSvc := &MockExchangeService{}
	trades := []*matching.Trade{
		{ID: uuid.New(), Symbol: "BTC-USD", Price: decimal.NewFromInt(50000), Quantity: decimal.NewFromFloat(0.1)},
	}
	mockSvc.On("GetRecentTrades", mock.Anything, "BTC-USD", 50).Return(trades, nil)

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/trades?symbol=BTC-USD", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Len(t, resp, 1)
}

// ============================================================
// Phase 8: 帳戶 API
// ============================================================

// 8.1 GET /accounts?user_id=... 正常回傳帳戶列表
func TestGetBalancesAPI_Success_ReturnsAccounts(t *testing.T) {
	mockSvc := &MockExchangeService{}
	userID := uuid.New()
	accounts := []*core.Account{
		{ID: uuid.New(), UserID: userID, Currency: "USD", Balance: decimal.NewFromInt(10000)},
		{ID: uuid.New(), UserID: userID, Currency: "BTC", Balance: decimal.NewFromFloat(0.5)},
	}
	mockSvc.On("GetBalances", mock.Anything, userID).Return(accounts, nil)

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/accounts?user_id="+userID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Len(t, resp, 2)
}

// 8.2 缺少 user_id 應返回 400
func TestGetBalancesAPI_MissingUserID_Returns400(t *testing.T) {
	mockSvc := &MockExchangeService{}
	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/accounts", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 8.3 user_id 格式不合法應返回 400
func TestGetBalancesAPI_InvalidUserID_Returns400(t *testing.T) {
	mockSvc := &MockExchangeService{}
	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/accounts?user_id=not-a-uuid", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 8.4 GetBalances Service 失敗應返回 500
func TestGetBalancesAPI_ServiceError_Returns500(t *testing.T) {
	mockSvc := &MockExchangeService{}
	userID := uuid.New()
	mockSvc.On("GetBalances", mock.Anything, userID).Return(nil, fmt.Errorf("資料庫錯誤"))

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/accounts?user_id="+userID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ============================================================
// Phase 9: 測試帳號 API (JoinArena)
// ============================================================

// 9.1 POST /test/join 成功建立匿名帳號
func TestJoinArenaAPI_Success_Returns201(t *testing.T) {
	mockSvc := &MockExchangeService{}
	user := &core.User{ID: uuid.New(), Email: "anon@test.com"}
	accounts := []*core.Account{
		{Currency: "USD", Balance: decimal.NewFromInt(10000)},
	}
	mockSvc.On("RegisterAnonymousUser", mock.Anything).Return(user, accounts, nil)

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test/join", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, user.ID.String(), resp["user_id"])
	mockSvc.AssertExpectations(t)
}

// 9.2 RegisterAnonymousUser 失敗時應返回 500
func TestJoinArenaAPI_ServiceError_Returns500(t *testing.T) {
	mockSvc := &MockExchangeService{}
	mockSvc.On("RegisterAnonymousUser", mock.Anything).Return((*core.User)(nil), ([]*core.Account)(nil), fmt.Errorf("無法建立用戶"))

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/test/join", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ============================================================
// Phase 10: 模擬器 API
// ============================================================

// 10.1 清除模擬資料成功
func TestClearSimulationDataAPI_Success_Returns200(t *testing.T) {
	mockSvc := &MockExchangeService{}
	mockSvc.On("ClearSimulationData", mock.Anything).Return(nil)

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/simulation/data", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockSvc.AssertExpectations(t)
}

// 10.2 清除模擬資料失敗時應返回 500
func TestClearSimulationDataAPI_ServiceError_Returns500(t *testing.T) {
	mockSvc := &MockExchangeService{}
	mockSvc.On("ClearSimulationData", mock.Anything).Return(fmt.Errorf("資料庫錯誤"))

	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/simulation/data", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// 10.3 未注入 simulator 時啟動模擬應返回 503
func TestStartSimulationAPI_SimulatorDisabled_Returns503(t *testing.T) {
	mockSvc := &MockExchangeService{}
	// setupRouter 傳入 nil 作為 simulator
	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/simulation/start", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// 10.4 未注入 simulator 時停止模擬應返回 503
func TestStopSimulationAPI_SimulatorDisabled_Returns503(t *testing.T) {
	mockSvc := &MockExchangeService{}
	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/simulation/stop", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// 10.5 未注入 simulator 時查詢狀態應返回 503
func TestGetSimulationStatusAPI_SimulatorDisabled_Returns503(t *testing.T) {
	mockSvc := &MockExchangeService{}
	router := setupRouter(mockSvc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/simulation/status", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
