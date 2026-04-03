package api

import (
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

func setupAccountRouter(mockSvc *MockOrderService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.Default()
	handler := NewHandler(mockSvc, nil)
	handler.RegisterRoutes(router)
	return router
}

func TestGetBalancesAPI_Success_ReturnsAccounts(t *testing.T) {
	userID := uuid.New()
	accounts := []*domain.Account{
		{
			ID:        uuid.New(),
			UserID:    userID,
			Currency:  "USD",
			Balance:   decimal.NewFromFloat(10000),
			Locked:    decimal.Zero,
			UpdatedAt: time.Now().UnixMilli(),
		},
	}

	mockSvc := &MockOrderService{}
	mockSvc.On("GetBalances", mock.Anything, userID).Return(accounts, nil)
	router := setupAccountRouter(mockSvc)

	req, _ := http.NewRequest("GET", "/accounts?user_id="+userID.String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Len(t, resp, 1)

	mockSvc.AssertExpectations(t)
}

func TestGetBalancesAPI_MissingUserID_Returns400(t *testing.T) {
	router := setupAccountRouter(nil)

	req, _ := http.NewRequest("GET", "/accounts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetBalancesAPI_InvalidUserID_Returns400(t *testing.T) {
	router := setupAccountRouter(nil)

	req, _ := http.NewRequest("GET", "/accounts?user_id=invalid-uuid", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetBalancesAPI_ServiceError_Returns500(t *testing.T) {
	userID := uuid.New()

	mockSvc := &MockOrderService{}
	mockSvc.On("GetBalances", mock.Anything, userID).Return(nil, fmt.Errorf("資料庫錯誤"))
	router := setupAccountRouter(mockSvc)

	req, _ := http.NewRequest("GET", "/accounts?user_id="+userID.String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestJoinArenaAPI_Success_Returns201(t *testing.T) {
	mockUser := &domain.User{
		ID:        uuid.New(),
		Email:     "anonymous_user@example.com",
		CreatedAt: time.Now().UnixMilli(),
	}
	mockAccounts := []*domain.Account{
		{Currency: "USD", Balance: decimal.NewFromFloat(1000000)},
		{Currency: "BTC", Balance: decimal.NewFromFloat(100)},
	}

	mockSvc := &MockOrderService{}
	mockSvc.On("RegisterAnonymousUser", mock.Anything).Return(mockUser, mockAccounts, nil)
	router := setupAccountRouter(mockSvc)

	req, _ := http.NewRequest("POST", "/test/join", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)

	assert.Equal(t, mockUser.ID.String(), resp["user_id"])
	balances := resp["balances"].(map[string]interface{})
	assert.Equal(t, "1000000", balances["USD"])
	assert.Equal(t, "100", balances["BTC"])

	mockSvc.AssertExpectations(t)
}

func TestJoinArenaAPI_ServiceError_Returns500(t *testing.T) {
	mockSvc := &MockOrderService{}
	mockSvc.On("RegisterAnonymousUser", mock.Anything).Return((*domain.User)(nil), ([]*domain.Account)(nil), fmt.Errorf("無法建立用戶"))
	router := setupAccountRouter(mockSvc)

	req, _ := http.NewRequest("POST", "/test/join", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
