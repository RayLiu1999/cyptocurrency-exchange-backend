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
		Symbol: "BTC-USD",
		Side:   SideBuy,
	}

	// Mock expectations
	orderRepo.On("GetOrderForUpdate", ctx, makerOrderID).Return(makerOrder, nil)
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
		Symbol: "BTC-USD",
		Side:   SideBuy,
	}

	// Mock expectations
	orderRepo.On("GetOrderForUpdate", ctx, makerOrderID).Return(makerOrder, nil)
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
		Symbol: "BTC-USD",
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
		Symbol: "BTC-USD",
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
// Phase 3: 成交步驟失敗場景 & 快照恢復
// ============================================================

func TestProcessTrade_StepFails_TransactionRollsBack(t *testing.T) {
	// Arrange：GetOrderForUpdate 成功，但 UpdateOrder 失敗，後續步驟不應被執行
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
		Symbol: "BTC-USD",
		Side:   SideBuy,
	}

	// GetOrderForUpdate 成功，UpdateOrder 模擬寫入失敗
	orderRepo.On("GetOrderForUpdate", ctx, makerOrderID).Return(makerOrder, nil)
	orderRepo.On("UpdateOrder", ctx, mock.AnythingOfType("*core.Order")).Return(fmt.Errorf("DB 更新失敗：死鎖"))

	// Act
	err := svc.ProcessTrade(ctx, trade, takerOrder)

	// Assert：ProcessTrade 應傳回錯誤
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "更新 Maker 訂單失敗")
	// 驗證後續步驟（資金結算、成交記錄寫入）均未被呼叫
	// 在真實 DB 事務中，這些未執行的步驟代表 ROLLBACK 可保證原子性
	accountRepo.AssertNotCalled(t, "UnlockFunds", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	tradeRepo.AssertNotCalled(t, "CreateTrade", mock.Anything, mock.Anything)
}

func TestRestoreEngineSnapshot_Success_RebuildsActiveOrders(t *testing.T) {
	// Arrange：模擬資料庫中有 2 筆活動訂單（一買一賣），服務啟動後應重建至記憶體引擎
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockUserRepository{}, &MockDBTransaction{}, "BTC-USD", nil)

	userID := uuid.New()
	activeOrders := []*Order{
		{
			ID:             uuid.New(),
			UserID:         userID,
			Symbol:         "BTC-USD",
			Side:           SideBuy,
			Type:           TypeLimit,
			Price:          decimal.NewFromInt(49000),
			Quantity:       decimal.NewFromInt(2),
			FilledQuantity: decimal.Zero,
			Status:         StatusNew,
		},
		{
			ID:             uuid.New(),
			UserID:         uuid.New(), // 不同使用者，確保非自成交
			Symbol:         "BTC-USD",
			Side:           SideSell,
			Type:           TypeLimit,
			Price:          decimal.NewFromInt(51000),
			Quantity:       decimal.NewFromInt(1),
			FilledQuantity: decimal.Zero,
			Status:         StatusNew,
		},
	}

	orderRepo.On("GetActiveOrders", ctx).Return(activeOrders, nil)

	// Act
	err := svc.RestoreEngineSnapshot(ctx)

	// Assert：無錯誤，且 OrderBook 應已重建出買賣各一筆掛單
	assert.NoError(t, err)
	snapshot, err := svc.GetOrderBook(ctx, "BTC-USD")
	assert.NoError(t, err)
	assert.Len(t, snapshot.Bids, 1, "應有 1 筆買單恢復至引擎")
	assert.Len(t, snapshot.Asks, 1, "應有 1 筆賣單恢復至引擎")
}

func TestRestoreEngineSnapshot_RepositoryError_ReturnsError(t *testing.T) {
	// Arrange：資料庫讀取失敗，快照恢復應立即中止並回傳錯誤
	ctx := context.Background()
	orderRepo := NewMockOrderRepository()
	accountRepo := NewMockAccountRepository()
	tradeRepo := NewMockTradeRepository()
	svc := NewExchangeService(orderRepo, accountRepo, tradeRepo, &MockUserRepository{}, &MockDBTransaction{}, "BTC-USD", nil)

	orderRepo.On("GetActiveOrders", ctx).Return(nil, fmt.Errorf("資料庫連線中斷"))

	// Act
	err := svc.RestoreEngineSnapshot(ctx)

	// Assert：應傳回包含診斷訊息的錯誤
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "載入活動訂單失敗")
}
