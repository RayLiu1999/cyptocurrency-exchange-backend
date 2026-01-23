package core

import (
	"context"
	"fmt"
	"log"
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
	userRepo      UserRepository // 新增
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
		userRepo:      userRepo, // 注入
		tradeListener: tradeListener,
		txManager:     txManager,
		engineManager: manager,
	}
}

// Ensure implementation
var _ ExchangeService = (*ExchangeServiceImpl)(nil)

// PlaceOrder 處理下單請求
func (s *ExchangeServiceImpl) PlaceOrder(ctx context.Context, order *Order) error {
	// 0. 規格化處理
	order.Symbol = strings.ToUpper(order.Symbol)
	order.Price = order.Price.Round(8)
	order.Quantity = order.Quantity.Round(8)

	// 1. 驗證訂單
	if order.Quantity.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("訂單數量無效")
	}
	// 限價單需驗證價格
	if order.Type == TypeLimit && order.Price.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("限價單價格無效")
	}

	// 2./3. 鎖定資金並建立訂單 (Atomic)
	currencyToLock, amountToLock := s.calculateLockAmount(order)

	order.ID = uuid.New()
	order.Status = StatusNew
	order.FilledQuantity = decimal.Zero
	order.CreatedAt = time.Now()
	order.UpdatedAt = time.Now()

	err := s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		// 2. 鎖定資金
		if err := s.accountRepo.LockFunds(ctx, order.UserID, currencyToLock, amountToLock); err != nil {
			return fmt.Errorf("餘額不足: %w", err)
		}

		// 3. 建立訂單
		if err := s.orderRepo.CreateOrder(ctx, order); err != nil {
			return fmt.Errorf("建立訂單失敗: %w", err)
		}
		return nil
	})

	if err != nil {
		return err
	}

	// 4. 撮合 - 根據交易對獲取對應引擎
	matchOrder := s.convertToMatchingOrder(order)
	engine := s.engineManager.GetEngine(order.Symbol)
	trades := engine.Process(matchOrder)

	// 5. 處理成交結果
	for _, trade := range trades {
		if err := s.ProcessTrade(ctx, trade, order); err != nil {
			log.Printf("處理成交失敗: %v", err)
			// TODO: 實作補償機制
		}
	}

	// 6. 更新訂單狀態
	filledQty := decimal.Zero
	for _, trade := range trades {
		filledQty = filledQty.Add(trade.Quantity)
	}
	order.FilledQuantity = filledQty

	if order.FilledQuantity.Equal(order.Quantity) {
		order.Status = StatusFilled
	} else if order.FilledQuantity.GreaterThan(decimal.Zero) {
		order.Status = StatusPartiallyFilled
	}

	order.UpdatedAt = time.Now()
	if err := s.orderRepo.UpdateOrder(ctx, order); err != nil {
		log.Printf("更新訂單狀態失敗: %v", err)
	}

	return nil
}

// calculateLockAmount 計算需要鎖定的資金
func (s *ExchangeServiceImpl) calculateLockAmount(order *Order) (currency string, amount decimal.Decimal) {
	base, quote := s.splitSymbol(order.Symbol)
	// BUY: 鎖定報價貨幣 (如 USD)
	// SELL: 鎖定基礎貨幣 (如 BTC/ETH)
	if order.Side == SideBuy {
		return quote, order.Price.Mul(order.Quantity)
	}
	return base, order.Quantity
}

// convertToMatchingOrder 轉換為撮合引擎訂單
func (s *ExchangeServiceImpl) convertToMatchingOrder(order *Order) *matching.Order {
	var side matching.OrderSide
	if order.Side == SideBuy {
		side = matching.SideBuy
	} else {
		side = matching.SideSell
	}

	var matchOrder *matching.Order
	if order.Type == TypeMarket {
		matchOrder = matching.NewMarketOrder(side, order.Quantity)
	} else {
		matchOrder = matching.NewOrder(side, order.Price, order.Quantity)
	}

	matchOrder.ID = order.ID
	return matchOrder
}

// ProcessTrade 處理成交結果
func (s *ExchangeServiceImpl) ProcessTrade(ctx context.Context, trade *matching.Trade, takerOrder *Order) error {
	log.Printf("成交: 價格=%s, 數量=%s, Maker=%s, Taker=%s",
		trade.Price, trade.Quantity, trade.MakerOrderID, trade.TakerOrderID)

	// 1. 取得 Maker 訂單
	makerOrder, err := s.orderRepo.GetOrder(ctx, trade.MakerOrderID)
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

	makerOrder.UpdatedAt = time.Now()

	// 4. 儲存 Maker 訂單
	if err := s.orderRepo.UpdateOrder(ctx, makerOrder); err != nil {
		return fmt.Errorf("更新 Maker 訂單失敗: %w", err)
	}

	// 5. 結算資金
	if err := s.SettleTrade(ctx, trade, takerOrder, makerOrder); err != nil {
		return fmt.Errorf("結算失敗: %w", err)
	}

	// 6. 儲存成交記錄 (持久化)
	if err := s.tradeRepo.CreateTrade(ctx, trade); err != nil {
		return fmt.Errorf("建立成交記錄失敗: %w", err)
	}

	// 7. 發送成交事件 (如果有設定監聽器)
	if s.tradeListener != nil {
		s.tradeListener.OnTrade(trade)
	}

	return nil
}

// SettleTrade 結算資金
func (s *ExchangeServiceImpl) SettleTrade(ctx context.Context, trade *matching.Trade, takerOrder, makerOrder *Order) error {
	tradeValue := trade.Price.Mul(trade.Quantity)

	// 買方：解鎖報價貨幣 (USD)，獲得基礎貨幣 (BTC)
	// 賣方：解鎖基礎貨幣 (BTC)，獲得報價貨幣 (USD)

	var buyer, seller *Order
	if takerOrder.Side == SideBuy {
		buyer = takerOrder
		seller = makerOrder
	} else {
		buyer = makerOrder
		seller = takerOrder
	}

	// 解析幣種
	base, quote := s.splitSymbol(takerOrder.Symbol)

	// 計算買方解鎖金額 (需考慮市價單與限價單更好價格的情況)
	buyerUnlockAmount := tradeValue
	if buyer.ID == takerOrder.ID {
		// Buyer is Taker
		if takerOrder.Type == TypeMarket {
			buyerUnlockAmount = decimal.Zero
		} else {
			// Limit Order Taker: 解鎖當初鎖定的金額 (可能高於成交額)
			buyerUnlockAmount = takerOrder.Price.Mul(trade.Quantity)
		}
	}
	// Round ensuring precision match with DB
	buyerUnlockAmount = buyerUnlockAmount.Round(8)
	tradeValue = tradeValue.Round(8)
	tradeQty := trade.Quantity.Round(8)

	// 買方結算
	// 1. 解鎖 Quote (USD)
	if err := s.accountRepo.UnlockFunds(ctx, buyer.UserID, quote, buyerUnlockAmount); err != nil {
		return fmt.Errorf("解鎖買方 %s 失敗: %w", quote, err)
	}
	// 2. 扣除花費 Quote (USD)
	if err := s.accountRepo.UpdateBalance(ctx, buyer.UserID, quote, tradeValue.Neg()); err != nil {
		return fmt.Errorf("扣除買方 %s 失敗: %w", quote, err)
	}
	// 3. 增加獲得 Base (ETH)
	if err := s.accountRepo.UpdateBalance(ctx, buyer.UserID, base, tradeQty); err != nil {
		return fmt.Errorf("增加買方 %s 失敗: %w", base, err)
	}

	// 賣方結算
	// 1. 解鎖 Base (ETH)
	if err := s.accountRepo.UnlockFunds(ctx, seller.UserID, base, tradeQty); err != nil {
		return fmt.Errorf("解鎖賣方 %s 失敗: %w", base, err)
	}
	// 2. 扣除賣出 Base (ETH)
	if err := s.accountRepo.UpdateBalance(ctx, seller.UserID, base, tradeQty.Neg()); err != nil {
		return fmt.Errorf("扣除賣方 %s 失敗: %w", base, err)
	}
	// 3. 增加獲得 Quote (USD)
	if err := s.accountRepo.UpdateBalance(ctx, seller.UserID, quote, tradeValue); err != nil {
		return fmt.Errorf("增加賣方 %s 失敗: %w", quote, err)
	}

	return nil
}

// GetOrder 取得訂單
func (s *ExchangeServiceImpl) GetOrder(ctx context.Context, id uuid.UUID) (*Order, error) {
	return s.orderRepo.GetOrder(ctx, id)
}

// GetOrdersByUser 取得用戶所有訂單
func (s *ExchangeServiceImpl) GetOrdersByUser(ctx context.Context, userID uuid.UUID) ([]*Order, error) {
	return s.orderRepo.GetOrdersByUser(ctx, userID)
}

// CancelOrder 取消訂單
func (s *ExchangeServiceImpl) CancelOrder(ctx context.Context, orderID, userID uuid.UUID) error {
	// 1. 取得訂單
	order, err := s.orderRepo.GetOrder(ctx, orderID)
	if err != nil {
		return fmt.Errorf("訂單不存在: %w", err)
	}

	// 2. 驗證權限
	if order.UserID != userID {
		return fmt.Errorf("權限不足")
	}

	// 3. 驗證狀態 (只有 NEW 或 PARTIALLY_FILLED 可取消)
	if order.Status == StatusFilled || order.Status == StatusCanceled {
		return fmt.Errorf("無法取消已完成或已取消的訂單")
	}

	// 4. 計算需解鎖的資金
	remainingQty := order.Quantity.Sub(order.FilledQuantity)
	currency, amountToUnlock := s.calculateUnlockAmount(order, remainingQty)

	// 5./6. 原子化解鎖與更新狀態
	err = s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		// 5. 解鎖資金
		if err := s.accountRepo.UnlockFunds(ctx, order.UserID, currency, amountToUnlock); err != nil {
			return fmt.Errorf("解鎖資金失敗: %w", err)
		}

		// 6. 更新訂單狀態
		order.Status = StatusCanceled
		order.UpdatedAt = time.Now()

		if err := s.orderRepo.UpdateOrder(ctx, order); err != nil {
			return fmt.Errorf("更新訂單狀態失敗: %w", err)
		}
		return nil
	})

	return err
}

// calculateUnlockAmount 計算取消時需解鎖的資金
func (s *ExchangeServiceImpl) calculateUnlockAmount(order *Order, remainingQty decimal.Decimal) (currency string, amount decimal.Decimal) {
	base, quote := s.splitSymbol(order.Symbol)
	if order.Side == SideBuy {
		return quote, order.Price.Mul(remainingQty)
	}
	return base, remainingQty
}

func (s *ExchangeServiceImpl) splitSymbol(symbol string) (base, quote string) {
	symbol = strings.ToUpper(symbol)
	parts := strings.Split(symbol, "-")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "BTC", "USD"
}

// GetOrderBook 取得即時訂單簿
func (s *ExchangeServiceImpl) GetOrderBook(ctx context.Context, symbol string) (*matching.OrderBookSnapshot, error) {
	// 1. 取得對應的引擎
	engine := s.engineManager.GetEngine(symbol)
	if engine == nil {
		// 為了簡化，如果引擎不存在就創建一個新的 (或返回錯誤)
		// 這裡假設這是一個 valid symbol
		return matching.NewOrderBookSnapshot(symbol), nil
	}

	// 2. 取得快照 (限制深度 20)
	return engine.GetOrderBookSnapshot(20), nil
}

// RegisterAnonymousUser 建立匿名用戶並發放測試金
func (s *ExchangeServiceImpl) RegisterAnonymousUser(ctx context.Context) (*User, []*Account, error) {
	newUserID := uuid.New()
	now := time.Now()

	user := &User{
		ID:           newUserID,
		Email:        fmt.Sprintf("anonymous_%s@test.com", newUserID.String()[:8]),
		PasswordHash: "N/A",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	currencies := []struct {
		code   string
		amount decimal.Decimal
	}{
		{"USD", decimal.NewFromInt(100000)},
		{"BTC", decimal.NewFromInt(10)},
		{"ETH", decimal.NewFromInt(100)},
	}

	var accounts []*Account

	err := s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		// 1. 建立 User
		if err := s.userRepo.CreateUser(ctx, user); err != nil {
			return fmt.Errorf("建立用戶失敗: %w", err)
		}

		// 2. 建立多個幣種的 Account 並給予初始資金
		for _, c := range currencies {
			acc := &Account{
				ID:        uuid.New(),
				UserID:    newUserID,
				Currency:  c.code,
				Balance:   c.amount,
				Locked:    decimal.Zero,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := s.accountRepo.CreateAccount(ctx, acc); err != nil {
				return fmt.Errorf("建立帳戶 %s 失敗: %w", c.code, err)
			}
			accounts = append(accounts, acc)
		}
		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	return user, accounts, nil
}

func (s *ExchangeServiceImpl) GetBalances(ctx context.Context, userID uuid.UUID) ([]*Account, error) {
	return s.accountRepo.GetAccountsByUser(ctx, userID)
}

func (s *ExchangeServiceImpl) GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]*KLine, error) {
	// 0. 規格化
	symbol = strings.ToUpper(symbol)
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.tradeRepo.GetKLines(ctx, symbol, interval, limit)
}

func (s *ExchangeServiceImpl) GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*matching.Trade, error) {
	symbol = strings.ToUpper(symbol)
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return s.tradeRepo.GetRecentTrades(ctx, symbol, limit)
}

// ClearSimulationData 清除交易資料
func (s *ExchangeServiceImpl) ClearSimulationData(ctx context.Context) error {
	return s.txManager.ExecTx(ctx, func(ctx context.Context) error {
		if err := s.tradeRepo.DeleteAllTrades(ctx); err != nil {
			return fmt.Errorf("清除成交資料失敗: %w", err)
		}
		if err := s.orderRepo.DeleteAllOrders(ctx); err != nil {
			return fmt.Errorf("清除訂單資料失敗: %w", err)
		}
		log.Println("✅ 已清除交易資料")
		return nil
	})
}
