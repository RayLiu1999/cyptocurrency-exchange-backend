package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// ============================================================
// Step 3: PlaceOrder 完整測試
// ============================================================

func TestPlaceOrder_InsufficientFunds_ReturnsError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockUserRepository{}, &MockDBTransaction{}, "BTC-USD", nil)

	order := &Order{
		UserID:   uuid.New(),
		Symbol:   "BTC-USD",
		Side:     SideBuy,
		Type:     TypeLimit,
		Price:    decimal.NewFromInt(50000),
		Quantity: decimal.NewFromInt(1),
	}

	// Mock: LockFunds 返回錯誤
	accountRepo.On("LockFunds", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(fmt.Errorf("insufficient funds"))

	// Act
	err := svc.PlaceOrder(ctx, order)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "餘額不足")
}

func TestPlaceOrder_Success_CreatesOrder(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockUserRepository{}, &MockDBTransaction{}, "BTC-USD", nil)

	order := &Order{
		UserID:   uuid.New(),
		Symbol:   "BTC-USD",
		Side:     SideBuy,
		Type:     TypeLimit,
		Price:    decimal.NewFromInt(50000),
		Quantity: decimal.NewFromInt(1),
	}

	// Mock expectations
	accountRepo.On("LockFunds", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	orderRepo.On("CreateOrder", mock.Anything, mock.Anything).Return(nil)
	orderRepo.On("UpdateOrder", mock.Anything, mock.Anything).Return(nil)

	// Act
	err := svc.PlaceOrder(ctx, order)

	// Assert
	assert.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, order.ID, "訂單應有 ID")
	assert.Equal(t, StatusNew, order.Status, "空 OrderBook 的訂單狀態應為 NEW")
	orderRepo.AssertCalled(t, "CreateOrder", mock.Anything, mock.Anything)
}

// ============================================================
// Phase 1.5: 取消訂單 (Cancel Order)
// ============================================================

func TestCancelOrder_Success_UnlocksFundsAndUpdatesStatus(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockUserRepository{}, &MockDBTransaction{}, "BTC-USD", nil)

	orderID := uuid.New()
	userID := uuid.New()
	existingOrder := &Order{
		ID:             orderID,
		UserID:         userID,
		Symbol:         "BTC-USD",
		Side:           SideBuy,
		Type:           TypeLimit,
		Price:          decimal.NewFromInt(50000),
		Quantity:       decimal.NewFromInt(10),
		FilledQuantity: decimal.NewFromInt(3), // 已成交 3，剩餘 7
		Status:         StatusPartiallyFilled,
	}

	orderRepo.On("GetOrder", ctx, orderID).Return(existingOrder, nil)
	orderRepo.On("UpdateOrder", ctx, mock.Anything).Return(nil)
	accountRepo.On("UnlockFunds", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Act
	err := svc.CancelOrder(ctx, orderID, userID)

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, StatusCanceled, existingOrder.Status, "訂單狀態應為 CANCELLED")
	accountRepo.AssertCalled(t, "UnlockFunds", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestCancelOrder_AlreadyFilled_ReturnsError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockUserRepository{}, &MockDBTransaction{}, "BTC-USD", nil)

	orderID := uuid.New()
	userID := uuid.New()
	filledOrder := &Order{
		ID:             orderID,
		UserID:         userID,
		Status:         StatusFilled, // 已完全成交
		FilledQuantity: decimal.NewFromInt(10),
		Quantity:       decimal.NewFromInt(10),
	}

	orderRepo.On("GetOrder", ctx, orderID).Return(filledOrder, nil)

	// Act
	err := svc.CancelOrder(ctx, orderID, userID)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "無法取消")
}

func TestCancelOrder_WrongUser_ReturnsError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockUserRepository{}, &MockDBTransaction{}, "BTC-USD", nil)

	orderID := uuid.New()
	ownerID := uuid.New()
	anotherUserID := uuid.New()
	order := &Order{
		ID:     orderID,
		UserID: ownerID,
		Status: StatusNew,
	}

	orderRepo.On("GetOrder", ctx, orderID).Return(order, nil)

	// Act
	err := svc.CancelOrder(ctx, orderID, anotherUserID)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "權限不足")
}
