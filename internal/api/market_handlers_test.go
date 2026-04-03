package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func setupMarketRouter(mockQuerySvc *MockQueryService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.Default()
	handler := NewHandler(nil, mockQuerySvc)
	handler.RegisterRoutes(router)
	return router
}

func TestGetOrderBookAPI_Success_ReturnsSnapshot(t *testing.T) {
	snapshot := &engine.OrderBookSnapshot{
		Symbol: "BTC-USD",
		Bids: []engine.OrderBookLevel{
			{Price: decimal.NewFromFloat(50000), Quantity: decimal.NewFromFloat(1)},
		},
		Asks: []engine.OrderBookLevel{
			{Price: decimal.NewFromFloat(50100), Quantity: decimal.NewFromFloat(1)},
		},
	}

	mockSvc := &MockQueryService{}
	mockSvc.On("GetOrderBook", mock.Anything, "BTC-USD").Return(snapshot, nil)
	router := setupMarketRouter(mockSvc)

	req, _ := http.NewRequest("GET", "/orderbook?symbol=BTC-USD&limit=20", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "BTC-USD", resp["symbol"])

	mockSvc.AssertExpectations(t)
}

func TestGetOrderBookAPI_ServiceError_Returns500(t *testing.T) {
	mockSvc := &MockQueryService{}
	mockSvc.On("GetOrderBook", mock.Anything, mock.AnythingOfType("string")).Return(nil, fmt.Errorf("內部錯誤"))
	router := setupMarketRouter(mockSvc)

	req, _ := http.NewRequest("GET", "/orderbook?symbol=BTC-USD", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetKLinesAPI_Success_ReturnsData(t *testing.T) {
	klines := []*domain.KLine{
		{
			Timestamp: time.Now().UnixMilli(),
			Open:      decimal.NewFromFloat(50000),
			High:      decimal.NewFromFloat(50100),
			Low:       decimal.NewFromFloat(49900),
			Close:     decimal.NewFromFloat(50050),
			Volume:    decimal.NewFromFloat(10),
		},
	}

	mockSvc := &MockQueryService{}
	mockSvc.On("GetKLines", mock.Anything, "BTC-USD", "1m", 100).Return(klines, nil)
	router := setupMarketRouter(mockSvc)

	req, _ := http.NewRequest("GET", "/klines?symbol=BTC-USD&interval=1m&limit=100", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Len(t, resp, 1)
	assert.Equal(t, "50000", resp[0]["open"])

	mockSvc.AssertExpectations(t)
}

func TestGetRecentTradesAPI_Success_ReturnsData(t *testing.T) {
	trades := []*engine.Trade{
		{
			ID:           uuid.New(),
			Symbol:       "BTC-USD",
			Price:        decimal.NewFromFloat(50000),
			Quantity:     decimal.NewFromFloat(1),
			MakerOrderID: uuid.New(),
			TakerOrderID: uuid.New(),
			CreatedAt:    time.Now().UnixMilli(),
		},
	}

	mockSvc := &MockQueryService{}
	mockSvc.On("GetRecentTrades", mock.Anything, "BTC-USD", 50).Return(trades, nil)
	router := setupMarketRouter(mockSvc)

	req, _ := http.NewRequest("GET", "/trades?symbol=BTC-USD&limit=50", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Len(t, resp, 1)

	mockSvc.AssertExpectations(t)
}
