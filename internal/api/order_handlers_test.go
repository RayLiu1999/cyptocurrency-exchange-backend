package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func setupOrderRouter(mockSvc *MockOrderService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.Default()
	handler := NewHandler(mockSvc, nil)
	handler.RegisterRoutes(router)
	return router
}

func TestPlaceOrderAPI_InvalidParams_Returns400(t *testing.T) {
	router := setupOrderRouter(nil)

	reqBody := []byte(`{"user_id": "", "symbol": "BTC-USD", "side": "BUY", "type": "LIMIT", "price": 50000, "quantity": 1}`)
	req, _ := http.NewRequest("POST", "/orders", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPlaceOrderAPI_Success_Returns202(t *testing.T) {
	mockSvc := &MockOrderService{}
	mockSvc.On("PlaceOrder", mock.Anything, mock.Anything).Return(nil)
	router := setupOrderRouter(mockSvc)

	userID := uuid.New().String()
	reqBody := []byte(fmt.Sprintf(`{"user_id": "%s", "symbol": "BTC-USD", "side": "BUY", "type": "LIMIT", "price": 50000, "quantity": 1}`, userID))
	req, _ := http.NewRequest("POST", "/orders", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NotEmpty(t, resp["order_id"])

	mockSvc.AssertExpectations(t)
}

func TestGetOrderAPI_Success_ReturnsOrder(t *testing.T) {
	orderID := uuid.New()
	userID := uuid.New()
	expectedOrder := &domain.Order{
		ID:             orderID,
		UserID:         userID,
		Symbol:         "BTC-USD",
		Side:           domain.SideBuy,
		Type:           domain.TypeLimit,
		Price:          decimal.NewFromFloat(50000),
		Quantity:       decimal.NewFromFloat(1),
		FilledQuantity: decimal.Zero,
		Status:         domain.StatusNew,
		CreatedAt:      time.Now().UnixMilli(),
	}

	mockSvc := &MockOrderService{}
	mockSvc.On("GetOrder", mock.Anything, orderID).Return(expectedOrder, nil)
	router := setupOrderRouter(mockSvc)

	req, _ := http.NewRequest("GET", "/orders/"+orderID.String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, orderID.String(), resp["id"])

	mockSvc.AssertExpectations(t)
}

func TestGetOrdersAPI_ByUserID_ReturnsOrders(t *testing.T) {
	userID := uuid.New()
	orders := []*domain.Order{
		{
			ID:             uuid.New(),
			UserID:         userID,
			Symbol:         "BTC-USD",
			Side:           domain.SideBuy,
			Type:           domain.TypeLimit,
			Price:          decimal.NewFromFloat(50000),
			Quantity:       decimal.NewFromFloat(1),
			FilledQuantity: decimal.Zero,
			Status:         domain.StatusNew,
		},
	}

	mockSvc := &MockOrderService{}
	mockSvc.On("GetOrdersByUser", mock.Anything, userID).Return(orders, nil)
	router := setupOrderRouter(mockSvc)

	req, _ := http.NewRequest("GET", "/orders?user_id="+userID.String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Len(t, resp, 1)

	mockSvc.AssertExpectations(t)
}

func TestCancelOrderAPI_Success_Returns200(t *testing.T) {
	orderID := uuid.New()
	userID := uuid.New()

	mockSvc := &MockOrderService{}
	mockSvc.On("CancelOrder", mock.Anything, orderID, userID).Return(nil)
	router := setupOrderRouter(mockSvc)

	req, _ := http.NewRequest("DELETE", "/orders/"+orderID.String()+"?user_id="+userID.String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "訂單已取消", resp["message"])

	mockSvc.AssertExpectations(t)
}

func TestCancelOrderAPI_ServiceError_Returns400(t *testing.T) {
	orderID := uuid.New()
	userID := uuid.New()

	mockSvc := &MockOrderService{}
	mockSvc.On("CancelOrder", mock.Anything, orderID, userID).Return(fmt.Errorf("訂單已成交，不可取消"))
	router := setupOrderRouter(mockSvc)

	req, _ := http.NewRequest("DELETE", "/orders/"+orderID.String()+"?user_id="+userID.String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "訂單已成交，不可取消", resp["error"])

	mockSvc.AssertExpectations(t)
}
