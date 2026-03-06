package core

import (
	"context"
	"testing"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// ============================================================
// Step 1: Maker 訂單狀態更新
// ============================================================

func TestProcessTrade_MakerFilledQuantityIncreases(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockUserRepository{}, &MockDBTransaction{}, "BTC-USD", nil)

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
	tradeRepo.On("CreateTrade", ctx, trade).Return(nil)

	// Act
	err := svc.ProcessTrade(ctx, trade, takerOrder)

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, decimal.NewFromInt(3), makerOrder.FilledQuantity, "Maker filled_quantity 應增加 3")
}

func TestProcessTrade_MakerStatusUpdated(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockUserRepository{}, &MockDBTransaction{}, "BTC-USD", nil)

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
	tradeRepo.On("CreateTrade", ctx, trade).Return(nil)

	// Act
	err := svc.ProcessTrade(ctx, trade, takerOrder)

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, StatusFilled, makerOrder.Status, "Maker 完全成交後狀態應為 FILLED")
}

// ============================================================
// Step 2: 資金結算邏輯
// ============================================================

func TestSettleTrade_BuyerUnlocksUSDAndReceivesBTC(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockUserRepository{}, &MockDBTransaction{}, "BTC-USD", nil)

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

	accountRepo.On("UnlockFunds", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	accountRepo.On("UpdateBalance", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Act
	err := svc.SettleTrade(ctx, trade, takerOrder, makerOrder)

	// Assert
	assert.NoError(t, err)
	// 驗證呼叫了 2 次 UnlockFunds (買方 1 次 + 賣方 1 次)
	accountRepo.AssertNumberOfCalls(t, "UnlockFunds", 2)
	accountRepo.AssertNumberOfCalls(t, "UpdateBalance", 4)
}

func TestSettleTrade_SellerUnlocksBTCAndReceivesUSD(t *testing.T) {
	// Arrange
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockUserRepository{}, &MockDBTransaction{}, "BTC-USD", nil)

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
