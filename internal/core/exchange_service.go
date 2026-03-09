package core

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ExchangeServiceImpl 交易所服務實作
type ExchangeServiceImpl struct {
	orderRepo     OrderRepository
	accountRepo   AccountRepository
	tradeRepo     TradeRepository
	userRepo      UserRepository
	tradeListener TradeEventListener
	txManager     DBTransaction
	engineManager *matching.EngineManager
}

// NewExchangeService 建立交易所服務
func NewExchangeService(orderRepo OrderRepository, accountRepo AccountRepository, tradeRepo TradeRepository, userRepo UserRepository, txManager DBTransaction, defaultSymbol string, tradeListener TradeEventListener) *ExchangeServiceImpl {
	manager := matching.NewEngineManager()
	// 預先建立預設交易對的 Engine
	manager.GetEngine(defaultSymbol)
	return &ExchangeServiceImpl{
		orderRepo:     orderRepo,
		accountRepo:   accountRepo,
		tradeRepo:     tradeRepo,
		userRepo:      userRepo,
		tradeListener: tradeListener,
		txManager:     txManager,
		engineManager: manager,
	}
}

// Ensure implementation
var _ ExchangeService = (*ExchangeServiceImpl)(nil)

// ProcessTrade 處理成交結果，必須在事務內呼叫
func (s *ExchangeServiceImpl) ProcessTrade(ctx context.Context, trade *matching.Trade, takerOrder *Order) error {
	log.Printf("成交: 價格=%s, 數量=%s, Maker=%s, Taker=%s",
		trade.Price, trade.Quantity, trade.MakerOrderID, trade.TakerOrderID)

	// 1. 使用 FOR UPDATE 鎖取得 Maker 訂單，防止 CancelOrder 競態條件
	makerOrder, err := s.orderRepo.GetOrderForUpdate(ctx, trade.MakerOrderID)
	if err != nil {
		return fmt.Errorf("取得 Maker 訂單失敗: %w", err)
	}

	// 2. 更新 Maker filled_quantity
	makerOrder.FilledQuantity = makerOrder.FilledQuantity.Add(trade.Quantity)

	// 3. 更新 Maker 狀態
	if makerOrder.FilledQuantity.Equal(makerOrder.Quantity) {
		makerOrder.Status = StatusFilled
	} else if makerOrder.FilledQuantity.GreaterThan(decimal.Zero) {
		makerOrder.Status = StatusPartiallyFilled
	}

	makerOrder.UpdatedAt = time.Now().UnixMilli()

	// 4. 儲存 Maker 訂單（確保 FilledQuantity 以當次 Delta 累加已在上方計算）
	if err := s.orderRepo.UpdateOrder(ctx, makerOrder); err != nil {
		return fmt.Errorf("更新 Maker 訂單失敗: %w", err)
	}

	if s.tradeListener != nil {
		s.tradeListener.OnOrderUpdate(makerOrder)
	}

	// 5. 結算資金
	if err := s.SettleTrade(ctx, trade, takerOrder, makerOrder); err != nil {
		return fmt.Errorf("結算失敗: %w", err)
	}

	// 6. 儲存成交記錄
	if err := s.tradeRepo.CreateTrade(ctx, trade); err != nil {
		return fmt.Errorf("建立成交記錄失敗: %w", err)
	}

	// 7. 發送成交事件
	if s.tradeListener != nil {
		s.tradeListener.OnTrade(trade)
	}

	return nil
}

// SettleTrade 結算資金
func (s *ExchangeServiceImpl) SettleTrade(ctx context.Context, trade *matching.Trade, takerOrder, makerOrder *Order) error {
	tradeValue := trade.Price.Mul(trade.Quantity)

	var buyer, seller *Order
	if takerOrder.Side == SideBuy {
		buyer = takerOrder
		seller = makerOrder
	} else {
		buyer = makerOrder
		seller = takerOrder
	}

	base, quote, err := s.splitSymbol(takerOrder.Symbol)
	if err != nil {
		return err
	}

	// 計算買方解鎖金額
	buyerUnlockAmount := tradeValue
	if buyer.ID == takerOrder.ID {
		if takerOrder.Type == TypeMarket {
			buyerUnlockAmount = tradeValue
		} else if !takerOrder.Price.IsZero() {
			buyerUnlockAmount = takerOrder.Price.Mul(trade.Quantity)
		}
	}

	buyerUnlockAmount = buyerUnlockAmount.Round(8)
	tradeValue = tradeValue.Round(8)
	tradeQty := trade.Quantity.Round(8)

	type accountUpdate struct {
		userID   uuid.UUID
		currency string
		amount   decimal.Decimal
		unlock   decimal.Decimal
	}

	updates := []accountUpdate{
		{userID: buyer.UserID, currency: quote, amount: tradeValue.Neg(), unlock: buyerUnlockAmount},
		{userID: buyer.UserID, currency: base, amount: tradeQty, unlock: decimal.Zero},
		{userID: seller.UserID, currency: base, amount: tradeQty.Neg(), unlock: tradeQty},
		{userID: seller.UserID, currency: quote, amount: tradeValue, unlock: decimal.Zero},
	}

	sort.Slice(updates, func(i, j int) bool {
		if updates[i].userID.String() != updates[j].userID.String() {
			return updates[i].userID.String() < updates[j].userID.String()
		}
		return updates[i].currency < updates[j].currency
	})

	for _, up := range updates {
		if up.unlock.GreaterThan(decimal.Zero) {
			if err := s.accountRepo.UnlockFunds(ctx, up.userID, up.currency, up.unlock); err != nil {
				return fmt.Errorf("解鎖用戶 %s 的 %s 失敗: %w", up.userID, up.currency, err)
			}
		}
		if !up.amount.IsZero() {
			if err := s.accountRepo.UpdateBalance(ctx, up.userID, up.currency, up.amount); err != nil {
				return fmt.Errorf("更新用戶 %s 的 %s 餘額失敗: %w", up.userID, up.currency, err)
			}
		}
	}

	return nil
}

// splitSymbol 將交易對拆分成 base 和 quote
func (s *ExchangeServiceImpl) splitSymbol(symbol string) (base, quote string, err error) {
	symbol = strings.ToUpper(symbol)
	parts := strings.Split(symbol, "-")
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	return "", "", fmt.Errorf("無效的交易對格式: %s", symbol)
}
