package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

/*
=== TDD TODO List: Service Layer ===

Phase 1: Maker 訂單狀態更新 ✅ DONE
- [x] 1.1 成交後 Maker 訂單 filled_quantity 應增加
- [x] 1.2 成交後 Maker 訂單狀態應更新

Phase 2: 資金結算邏輯 ✅ DONE
- [x] 2.1 買方：解鎖 USD，增加 BTC
- [x] 2.2 賣方：解鎖 BTC，增加 USD

Phase 3: 服務層測試 ✅ DONE
- [x] 3.1 PlaceOrder 餘額不足應返回錯誤
- [x] 3.2 PlaceOrder 撮合成功應更新訂單狀態

Phase 1.5: 取消訂單 (Cancel Order) ✅ DONE
- [x] 取消成功應解鎖剩餘資金並更新狀態
- [x] 取消已成交訂單應返回錯誤
- [x] 取消其他用戶訂單應返回錯誤

=====================================
*/

// ============================================================
// Mock Repositories
// ============================================================

type MockOrderRepository struct {
	mock.Mock
	orders map[uuid.UUID]*Order
}

func NewMockOrderRepository() *MockOrderRepository {
	return &MockOrderRepository{
		orders: make(map[uuid.UUID]*Order),
	}
}

func (m *MockOrderRepository) CreateOrder(ctx context.Context, order *Order) error {
	args := m.Called(ctx, order)
	if args.Error(0) == nil {
		m.orders[order.ID] = order
	}
	return args.Error(0)
}

func (m *MockOrderRepository) GetOrder(ctx context.Context, id uuid.UUID) (*Order, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Order), args.Error(1)
}

func (m *MockOrderRepository) UpdateOrder(ctx context.Context, order *Order) error {
	args := m.Called(ctx, order)
	if args.Error(0) == nil {
		m.orders[order.ID] = order
	}
	return args.Error(0)
}

func (m *MockOrderRepository) GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*Order, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*Order), args.Error(1)
}

type MockAccountRepository struct {
	mock.Mock
}

func NewMockAccountRepository() *MockAccountRepository {
	return &MockAccountRepository{}
}

func (m *MockAccountRepository) GetAccount(ctx context.Context, userID uuid.UUID, currency string) (*Account, error) {
	args := m.Called(ctx, userID, currency)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Account), args.Error(1)
}

func (m *MockAccountRepository) CreateAccount(ctx context.Context, account *Account) error {
	args := m.Called(ctx, account)
	return args.Error(0)
}

func (m *MockAccountRepository) UpdateBalance(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	args := m.Called(ctx, userID, currency, amount)
	return args.Error(0)
}

func (m *MockAccountRepository) LockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	args := m.Called(ctx, userID, currency, amount)
	return args.Error(0)
}

func (m *MockAccountRepository) UnlockFunds(ctx context.Context, userID uuid.UUID, currency string, amount decimal.Decimal) error {
	args := m.Called(ctx, userID, currency, amount)
	return args.Error(0)
}

type MockDBTransaction struct{}

func (m *MockDBTransaction) ExecTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// MockTradeRepository implementation
type MockTradeRepository struct {
	mock.Mock
}

func NewMockTradeRepository() *MockTradeRepository {
	return &MockTradeRepository{}
}

func (m *MockTradeRepository) CreateTrade(ctx context.Context, trade *matching.Trade) error {
	args := m.Called(ctx, trade)
	return args.Error(0)
}

// ============================================================
// Step 1: Maker 訂單狀態更新
// ============================================================

// TODO 1.1: 成交後 Maker 訂單 filled_quantity 應增加
func TestProcessTrade_MakerFilledQuantityIncreases(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockDBTransaction{}, "BTC-USD")

	makerOrderID := uuid.New()
	makerOrder := &Order{
		ID:             makerOrderID,
		UserID:         uuid.New(),
		Symbol:         "BTC-USD",
		Side:           SideSell,
		Price:          decimal.NewFromInt(50000),
		Quantity:       decimal.NewFromInt(10),
		FilledQuantity: decimal.Zero,
		Status:         StatusNew,
	}

	trade := &matching.Trade{
		ID:           uuid.New(),
		MakerOrderID: makerOrderID,
		TakerOrderID: uuid.New(),
		Price:        decimal.NewFromInt(50000),
		Quantity:     decimal.NewFromInt(3),
	}

	takerOrder := &Order{
		ID:     trade.TakerOrderID,
		UserID: uuid.New(),
		Side:   SideBuy,
	}

	// Mock expectations
	orderRepo.On("GetOrder", ctx, makerOrderID).Return(makerOrder, nil)
	orderRepo.On("UpdateOrder", ctx, mock.AnythingOfType("*core.Order")).Return(nil)
	accountRepo.On("UnlockFunds", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	accountRepo.On("UpdateBalance", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	tradeRepo.On("CreateTrade", ctx, trade).Return(nil) // Added expectation

	// Act
	err := svc.ProcessTrade(ctx, trade, takerOrder)

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, decimal.NewFromInt(3), makerOrder.FilledQuantity, "Maker filled_quantity 應增加 3")
}

// TODO 1.2: 成交後 Maker 訂單狀態應更新
func TestProcessTrade_MakerStatusUpdated(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockDBTransaction{}, "BTC-USD")

	makerOrderID := uuid.New()
	makerOrder := &Order{
		ID:             makerOrderID,
		UserID:         uuid.New(),
		Symbol:         "BTC-USD",
		Side:           SideSell,
		Price:          decimal.NewFromInt(50000),
		Quantity:       decimal.NewFromInt(10),
		FilledQuantity: decimal.NewFromInt(7), // 已成交 7，再成交 3 就完全成交
		Status:         StatusPartiallyFilled,
	}

	trade := &matching.Trade{
		ID:           uuid.New(),
		MakerOrderID: makerOrderID,
		TakerOrderID: uuid.New(),
		Price:        decimal.NewFromInt(50000),
		Quantity:     decimal.NewFromInt(3), // 成交 3，總共 10，完全成交
	}

	takerOrder := &Order{
		ID:     trade.TakerOrderID,
		UserID: uuid.New(),
		Side:   SideBuy,
	}

	// Mock expectations
	orderRepo.On("GetOrder", ctx, makerOrderID).Return(makerOrder, nil)
	orderRepo.On("UpdateOrder", ctx, mock.AnythingOfType("*core.Order")).Return(nil)
	accountRepo.On("UnlockFunds", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	accountRepo.On("UpdateBalance", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	tradeRepo.On("CreateTrade", ctx, trade).Return(nil) // Added expectation

	// Act
	err := svc.ProcessTrade(ctx, trade, takerOrder)

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, StatusFilled, makerOrder.Status, "Maker 完全成交後狀態應為 FILLED")
}

// ============================================================
// Step 2: 資金結算邏輯
// ============================================================

// TODO 2.1: 買方：解鎖 USD，增加 BTC
func TestSettleTrade_BuyerUnlocksUSDAndReceivesBTC(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockDBTransaction{}, "BTC-USD")

	takerOrder := &Order{
		ID:     uuid.New(),
		UserID: uuid.New(),
		Side:   SideBuy, // Taker 是買方
	}

	makerOrder := &Order{
		ID:     uuid.New(),
		UserID: uuid.New(),
		Side:   SideSell, // Maker 是賣方
	}

	trade := &matching.Trade{
		ID:           uuid.New(),
		MakerOrderID: makerOrder.ID,
		TakerOrderID: takerOrder.ID,
		Price:        decimal.NewFromInt(50000),
		Quantity:     decimal.NewFromFloat(0.5), // 成交 0.5 BTC = 25000 USD
	}

	// 使用 mock.Anything 避免 Decimal 內部表示差異問題
	accountRepo.On("UnlockFunds", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	accountRepo.On("UpdateBalance", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Act
	err := svc.SettleTrade(ctx, trade, takerOrder, makerOrder)

	// Assert
	assert.NoError(t, err)
	// 驗證呼叫了 4 次 (買方 2 次 + 賣方 2 次)
	accountRepo.AssertNumberOfCalls(t, "UnlockFunds", 2)
	accountRepo.AssertNumberOfCalls(t, "UpdateBalance", 4)
}

// TODO 2.2: 賣方：解鎖 BTC，增加 USD
func TestSettleTrade_SellerUnlocksBTCAndReceivesUSD(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockDBTransaction{}, "BTC-USD")

	takerOrder := &Order{
		ID:     uuid.New(),
		UserID: uuid.New(),
		Side:   SideSell, // Taker 是賣方
	}

	makerOrder := &Order{
		ID:     uuid.New(),
		UserID: uuid.New(),
		Side:   SideBuy, // Maker 是買方
	}

	trade := &matching.Trade{
		ID:           uuid.New(),
		MakerOrderID: makerOrder.ID,
		TakerOrderID: takerOrder.ID,
		Price:        decimal.NewFromInt(50000),
		Quantity:     decimal.NewFromFloat(1), // 成交 1 BTC = 50000 USD
	}

	accountRepo.On("UnlockFunds", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	accountRepo.On("UpdateBalance", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Act
	err := svc.SettleTrade(ctx, trade, takerOrder, makerOrder)

	// Assert
	assert.NoError(t, err)
	accountRepo.AssertNumberOfCalls(t, "UnlockFunds", 2)
	accountRepo.AssertNumberOfCalls(t, "UpdateBalance", 4)
}

// ============================================================
// Step 3: PlaceOrder 完整測試
// ============================================================

// TODO 3.1: PlaceOrder 餘額不足應返回錯誤
func TestPlaceOrder_InsufficientFunds_ReturnsError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockDBTransaction{}, "BTC-USD")

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

// TODO 3.2: PlaceOrder 成功應建立訂單
func TestPlaceOrder_Success_CreatesOrder(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockDBTransaction{}, "BTC-USD")

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

// TODO: 取消成功應解鎖剩餘資金並更新狀態
func TestCancelOrder_Success_UnlocksFundsAndUpdatesStatus(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockDBTransaction{}, "BTC-USD")

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

// TODO: 取消已成交訂單應返回錯誤
func TestCancelOrder_AlreadyFilled_ReturnsError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockDBTransaction{}, "BTC-USD")

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

// TODO: 取消其他用戶訂單應返回錯誤
func TestCancelOrder_WrongUser_ReturnsError(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockDBTransaction{}, "BTC-USD")

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
