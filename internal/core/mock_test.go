package core

import (
	"context"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/mock"
)

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

func (m *MockOrderRepository) GetActiveOrders(ctx context.Context) ([]*Order, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*Order), args.Error(1)
}

func (m *MockOrderRepository) GetOrderForUpdate(ctx context.Context, id uuid.UUID) (*Order, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Order), args.Error(1)
}

func (m *MockOrderRepository) DeleteAllOrders(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
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

func (m *MockAccountRepository) GetAccountsByUser(ctx context.Context, userID uuid.UUID) ([]*Account, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*Account), args.Error(1)
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

func (m *MockTradeRepository) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*KLine, error) {
	args := m.Called(ctx, symbol, interval, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*KLine), args.Error(1)
}

func (m *MockTradeRepository) GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*matching.Trade, error) {
	args := m.Called(ctx, symbol, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*matching.Trade), args.Error(1)
}

func (m *MockTradeRepository) DeleteAllTrades(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockTradeRepository) TradeExistsByID(ctx context.Context, id uuid.UUID) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

// MockUserRepository implementation
type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) CreateUser(ctx context.Context, user *User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockUserRepository) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*User), args.Error(1)
}
