package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

// ============================================================
// Step 1: 資金結算計算
// ============================================================

func TestCalculateTradeSettlement_BuyerReceivesBTC(t *testing.T) {
	// Arrange
	svc := NewExchangeService(nil, nil, nil, nil, nil, "BTC-USD", nil)

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

	// Act
	updates, err := svc.CalculateTradeSettlement(trade, takerOrder, makerOrder)

	// Assert
	assert.NoError(t, err)
	assert.Len(t, updates, 4)

	// 檢查買方解鎖與獲得資金
	assert.Equal(t, takerOrder.UserID, updates[0].UserID)
	assert.Equal(t, "USD", updates[0].Currency)
	assert.True(t, updates[0].Amount.Equal(decimal.NewFromInt(-25000)))
	assert.True(t, updates[0].Unlock.Equal(decimal.NewFromInt(25000)))

	assert.Equal(t, takerOrder.UserID, updates[1].UserID)
	assert.Equal(t, "BTC", updates[1].Currency)
	assert.True(t, updates[1].Amount.Equal(decimal.NewFromFloat(0.5)))
	assert.True(t, updates[1].Unlock.Equal(decimal.Zero))

	// 檢查賣方解鎖與獲得資金
	assert.Equal(t, makerOrder.UserID, updates[2].UserID)
	assert.Equal(t, "BTC", updates[2].Currency)
	assert.True(t, updates[2].Amount.Equal(decimal.NewFromFloat(-0.5)))
	assert.True(t, updates[2].Unlock.Equal(decimal.NewFromFloat(0.5)))

	assert.Equal(t, makerOrder.UserID, updates[3].UserID)
	assert.Equal(t, "USD", updates[3].Currency)
	assert.True(t, updates[3].Amount.Equal(decimal.NewFromInt(25000)))
	assert.True(t, updates[3].Unlock.Equal(decimal.Zero))
}

func TestCalculateTradeSettlement_SellerReceivesUSD(t *testing.T) {
	// Arrange
	svc := NewExchangeService(nil, nil, nil, nil, nil, "BTC-USD", nil)

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

	// Act
	updates, err := svc.CalculateTradeSettlement(trade, takerOrder, makerOrder)

	// Assert
	assert.NoError(t, err)
	assert.Len(t, updates, 4)
}

// ============================================================
// Step 2: 聚合與排序測試
// ============================================================
func TestAggregateAndSortAccountUpdates(t *testing.T) {
	userA := uuid.New()
	userB := uuid.New()

	// 若 userA > userB, 交換以保證字母順序
	if userA.String() > userB.String() {
		userA, userB = userB, userA
	}

	updates := []AccountUpdate{
		{UserID: userB, Currency: "BTC", Amount: decimal.NewFromFloat(1), Unlock: decimal.Zero},
		{UserID: userA, Currency: "USD", Amount: decimal.NewFromInt(100), Unlock: decimal.NewFromInt(100)},
		{UserID: userB, Currency: "BTC", Amount: decimal.NewFromFloat(2), Unlock: decimal.Zero},
		{UserID: userA, Currency: "USD", Amount: decimal.NewFromInt(-50), Unlock: decimal.Zero},
		{UserID: userA, Currency: "BTC", Amount: decimal.NewFromFloat(1), Unlock: decimal.NewFromFloat(0.5)},
	}

	res := AggregateAndSortAccountUpdates(updates)

	// Order should be:
	// UserA BTC: Amount 1, Unlock 0.5
	// UserA USD: Amount 50, Unlock 100
	// UserB BTC: Amount 3, Unlock 0

	assert.Len(t, res, 3)

	assert.Equal(t, userA, res[0].UserID)
	assert.Equal(t, "BTC", res[0].Currency)
	assert.True(t, res[0].Amount.Equal(decimal.NewFromFloat(1)))
	assert.True(t, res[0].Unlock.Equal(decimal.NewFromFloat(0.5)))

	assert.Equal(t, userA, res[1].UserID)
	assert.Equal(t, "USD", res[1].Currency)
	assert.True(t, res[1].Amount.Equal(decimal.NewFromInt(50)))
	assert.True(t, res[1].Unlock.Equal(decimal.NewFromInt(100)))

	assert.Equal(t, userB, res[2].UserID)
	assert.Equal(t, "BTC", res[2].Currency)
	assert.True(t, res[2].Amount.Equal(decimal.NewFromFloat(3)))
	assert.True(t, res[2].Unlock.Equal(decimal.Zero))
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
