package api

import (
	"context"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

// MockOrderService mock order.OrderService for testing
type MockOrderService struct {
	mock.Mock
}

func (m *MockOrderService) PlaceOrder(ctx context.Context, order *domain.Order) error {
	args := m.Called(ctx, order)
	return args.Error(0)
}

func (m *MockOrderService) BatchPlaceOrders(ctx context.Context, orders []*domain.Order) error {
	args := m.Called(ctx, orders)
	return args.Error(0)
}

func (m *MockOrderService) CancelOrder(ctx context.Context, orderID, userID uuid.UUID) error {
	args := m.Called(ctx, orderID, userID)
	return args.Error(0)
}

func (m *MockOrderService) GetOrder(ctx context.Context, id uuid.UUID) (*domain.Order, error) {
	args := m.Called(ctx, id)
	if args.Get(0) != nil {
		return args.Get(0).(*domain.Order), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockOrderService) GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Order, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) != nil {
		return args.Get(0).([]*domain.Order), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockOrderService) RegisterAnonymousUser(ctx context.Context) (*domain.User, []*domain.Account, error) {
	args := m.Called(ctx)
	var user *domain.User
	if args.Get(0) != nil {
		user = args.Get(0).(*domain.User)
	}
	var accounts []*domain.Account
	if args.Get(1) != nil {
		accounts = args.Get(1).([]*domain.Account)
	}
	return user, accounts, args.Error(2)
}

func (m *MockOrderService) GetBalances(ctx context.Context, userID uuid.UUID) ([]*domain.Account, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) != nil {
		return args.Get(0).([]*domain.Account), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockOrderService) RechargeTestUser(ctx context.Context, userID uuid.UUID) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

// MockQueryService mock marketdata.QueryService for testing
type MockQueryService struct {
	mock.Mock
}

func (m *MockQueryService) GetOrderBook(ctx context.Context, symbol string) (*engine.OrderBookSnapshot, error) {
	args := m.Called(ctx, symbol)
	if args.Get(0) != nil {
		return args.Get(0).(*engine.OrderBookSnapshot), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockQueryService) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*domain.KLine, error) {
	args := m.Called(ctx, symbol, interval, limit)
	if args.Get(0) != nil {
		return args.Get(0).([]*domain.KLine), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockQueryService) GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*engine.Trade, error) {
	args := m.Called(ctx, symbol, limit)
	if args.Get(0) != nil {
		return args.Get(0).([]*engine.Trade), args.Error(1)
	}
	return nil, args.Error(1)
}
