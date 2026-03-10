//go:build integration

package core_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/RayLiu1999/exchange/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// 測試輔助工具
// =============================================================================

// setupE2EDB 建立測試資料庫連線，若無法連線則跳過測試
func setupE2EDB(t *testing.T) *repository.PostgresRepository {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:123qwe@localhost:5432/exchange?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skipf("skip: 無法建立連線池 (%v)", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("skip: 資料庫無法連線 (%v)", err)
	}
	t.Cleanup(func() { pool.Close() })
	return repository.NewPostgresRepository(pool)
}

// setupE2EService 建立完整服務堆疊（真實資料庫 + 真實撮合引擎）
// 每個呼叫都會產生一個獨立的 EngineManager，適合用於模擬重啟場景
func setupE2EService(repo *repository.PostgresRepository) *core.ExchangeServiceImpl {
	return core.NewExchangeService(repo, repo, repo, repo, repo, "BTC-USD", nil)
}

// e2eUser 代表一個在測試中使用的用戶（含 BTC 與 USD 帳戶）
type e2eUser struct {
	User *core.User
}

// createE2EUser 建立測試用戶並同時建立 BTC 和 USD 帳戶（含指定初始餘額）
// 測試結束後自動清理所有相關的 trades、orders、accounts、users 資料
func createE2EUser(t *testing.T, repo *repository.PostgresRepository, btcBalance, usdBalance decimal.Decimal) *e2eUser {
	t.Helper()
	ctx := context.Background()

	userID, err := uuid.NewV7()
	require.NoError(t, err)
	now := time.Now().UnixMilli()

	user := &core.User{
		ID:           userID,
		Email:        fmt.Sprintf("e2e-%s@test.local", userID),
		PasswordHash: "hash_not_used",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	require.NoError(t, repo.CreateUser(ctx, user))

	// 同時建立 BTC 與 USD 帳戶，確保 UpdateBalance 不會因帳戶不存在而靜默失敗
	for _, cur := range []struct {
		currency string
		balance  decimal.Decimal
	}{
		{"BTC", btcBalance},
		{"USD", usdBalance},
	} {
		acctID, _ := uuid.NewV7()
		require.NoError(t, repo.CreateAccount(ctx, &core.Account{
			ID:        acctID,
			UserID:    userID,
			Currency:  cur.currency,
			Balance:   cur.balance,
			Locked:    decimal.Zero,
			CreatedAt: now,
			UpdatedAt: now,
		}))
	}

	// 清理順序：trades → orders → accounts → users（遵守 FK 約束）
	t.Cleanup(func() {
		pool := repo.Pool()
		pool.Exec(ctx,
			`DELETE FROM trades
			 WHERE maker_order_id IN (SELECT id FROM orders WHERE user_id = $1)
			    OR taker_order_id IN (SELECT id FROM orders WHERE user_id = $1)`,
			userID)
		pool.Exec(ctx, `DELETE FROM orders WHERE user_id = $1`, userID)
		pool.Exec(ctx, `DELETE FROM accounts WHERE user_id = $1`, userID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	return &e2eUser{User: user}
}

// makeLimitOrderRequest 建立一個限價單請求物件（ID、Status 等由 PlaceOrder 填入）
func makeLimitOrderRequest(userID uuid.UUID, side core.OrderSide, price, qty string) *core.Order {
	return &core.Order{
		UserID:   userID,
		Symbol:   "BTC-USD",
		Side:     side,
		Type:     core.TypeLimit,
		Price:    decimal.RequireFromString(price),
		Quantity: decimal.RequireFromString(qty),
	}
}

// =============================================================================
// Phase 5: 端對端整合測試
// 驗證完整鏈路：Service → Repository → Matching Engine → Trade Persistence
// =============================================================================

// TestE2E_LimitOrder_FullMatch_TradePersistedAndFundsSettled
// 驗證兩筆限價單完全撮合後：
//  1. 成交記錄寫入資料庫
//  2. 雙方訂單狀態正確更新為 FILLED
//  3. 買賣雙方資金正確結算（解鎖 + 餘額更新）
func TestE2E_LimitOrder_FullMatch_TradePersistedAndFundsSettled(t *testing.T) {
	repo := setupE2EDB(t)
	svc := setupE2EService(repo)
	ctx := context.Background()

	// 賣家：持有 1 BTC，初始 USD 為 0
	seller := createE2EUser(t, repo, decimal.NewFromInt(1), decimal.Zero)
	// 買家：持有 $55,000 USD，初始 BTC 為 0
	buyer := createE2EUser(t, repo, decimal.Zero, decimal.NewFromInt(55000))

	// === Act: 賣家先掛限價賣單（Maker），買家後掛限價買單（Taker）===
	sellOrder := makeLimitOrderRequest(seller.User.ID, core.SideSell, "50000", "1")
	require.NoError(t, svc.PlaceOrder(ctx, sellOrder))

	buyOrder := makeLimitOrderRequest(buyer.User.ID, core.SideBuy, "50000", "1")
	require.NoError(t, svc.PlaceOrder(ctx, buyOrder))

	// === Assert: 訂單狀態應為 FILLED ===
	fetchedSell, err := repo.GetOrder(ctx, sellOrder.ID)
	require.NoError(t, err)
	assert.Equal(t, core.StatusFilled, fetchedSell.Status, "賣單應為 FILLED")
	assert.True(t, decimal.NewFromInt(1).Equal(fetchedSell.FilledQuantity), "賣單成交量應為 1 BTC")

	fetchedBuy, err := repo.GetOrder(ctx, buyOrder.ID)
	require.NoError(t, err)
	assert.Equal(t, core.StatusFilled, fetchedBuy.Status, "買單應為 FILLED")
	assert.True(t, decimal.NewFromInt(1).Equal(fetchedBuy.FilledQuantity), "買單成交量應為 1 BTC")

	// === Assert: 成交記錄應持久化至資料庫 ===
	trades, err := repo.GetRecentTrades(ctx, "BTC-USD", 10)
	require.NoError(t, err)
	var tradeFound bool
	for _, tr := range trades {
		if tr.MakerOrderID == sellOrder.ID && tr.TakerOrderID == buyOrder.ID {
			tradeFound = true
			assert.True(t, decimal.NewFromInt(50000).Equal(tr.Price), "成交價格應為 50000")
			assert.True(t, decimal.NewFromInt(1).Equal(tr.Quantity), "成交數量應為 1 BTC")
		}
	}
	assert.True(t, tradeFound, "應存在對應的成交記錄（Maker=sellOrder, Taker=buyOrder）")

	// === Assert: 資金結算 ===
	// 賣家成交後：BTC 全部賣出，USD 獲得成交金額
	sellerBTC, err := repo.GetAccount(ctx, seller.User.ID, "BTC")
	require.NoError(t, err)
	assert.True(t, decimal.Zero.Equal(sellerBTC.Balance), "賣家 BTC 可用餘額應為 0")
	assert.True(t, decimal.Zero.Equal(sellerBTC.Locked), "賣家 BTC 鎖定應為 0（已全部成交解鎖）")

	sellerUSD, err := repo.GetAccount(ctx, seller.User.ID, "USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(50000).Equal(sellerUSD.Balance), "賣家 USD 餘額應為 50000")

	// 買家成交後：USD 扣除成交金額，BTC 增加
	buyerBTC, err := repo.GetAccount(ctx, buyer.User.ID, "BTC")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(1).Equal(buyerBTC.Balance), "買家 BTC 餘額應為 1")

	buyerUSD, err := repo.GetAccount(ctx, buyer.User.ID, "USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(5000).Equal(buyerUSD.Balance), "買家 USD 餘額應為 5000 (55000-50000)")
	assert.True(t, decimal.Zero.Equal(buyerUSD.Locked), "買家 USD 鎖定應為 0（成交後應全部解鎖）")
}

// TestE2E_LimitOrder_PartialMatch_CorrectStatusAndFunds
// 驗證部分撮合場景：
//  1. 賣家掛 2 BTC，買家只吃 1 BTC
//  2. 賣家訂單狀態為 PARTIALLY_FILLED，剩餘 1 BTC 仍鎖定在掛簿
//  3. 買家訂單狀態為 FILLED
func TestE2E_LimitOrder_PartialMatch_CorrectStatusAndFunds(t *testing.T) {
	repo := setupE2EDB(t)
	svc := setupE2EService(repo)
	ctx := context.Background()

	// 賣家：2 BTC / $0 USD
	seller := createE2EUser(t, repo, decimal.NewFromInt(2), decimal.Zero)
	// 買家：0 BTC / $55,000 USD
	buyer := createE2EUser(t, repo, decimal.Zero, decimal.NewFromInt(55000))

	// 賣家掛 2 BTC 賣單（Maker），買家只買 1 BTC（Taker）
	sellOrder := makeLimitOrderRequest(seller.User.ID, core.SideSell, "50000", "2")
	require.NoError(t, svc.PlaceOrder(ctx, sellOrder))

	buyOrder := makeLimitOrderRequest(buyer.User.ID, core.SideBuy, "50000", "1")
	require.NoError(t, svc.PlaceOrder(ctx, buyOrder))

	// 買單應完全成交
	fetchedBuy, err := repo.GetOrder(ctx, buyOrder.ID)
	require.NoError(t, err)
	assert.Equal(t, core.StatusFilled, fetchedBuy.Status, "買單應為 FILLED")

	// 賣單應部分成交，剩餘 1 BTC 留在掛簿
	fetchedSell, err := repo.GetOrder(ctx, sellOrder.ID)
	require.NoError(t, err)
	assert.Equal(t, core.StatusPartiallyFilled, fetchedSell.Status, "賣單應為 PARTIALLY_FILLED")
	assert.True(t, decimal.NewFromInt(1).Equal(fetchedSell.FilledQuantity), "賣單成交量應為 1 BTC")

	// 賣家：2 BTC 全部鎖定（掛單時），成交 1 BTC 後：
	//   - 可用餘額 = 0（2 BTC 全被鎖定，解鎖 1 + 扣除 1 = 仍有 1 BTC 鎖定）
	//   - 鎖定 = 1（剩餘未成交部分）
	sellerBTC, err := repo.GetAccount(ctx, seller.User.ID, "BTC")
	require.NoError(t, err)
	assert.True(t, decimal.Zero.Equal(sellerBTC.Balance), "賣家可用 BTC 應為 0")
	assert.True(t, decimal.NewFromInt(1).Equal(sellerBTC.Locked), "賣家 BTC 鎖定（剩餘掛單）應為 1")

	// 賣家已收到部分成交款（1 BTC × $50,000 = $50,000）
	sellerUSD, err := repo.GetAccount(ctx, seller.User.ID, "USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(50000).Equal(sellerUSD.Balance), "賣家 USD 應為 50000（部分成交金額）")
}

// TestE2E_MarketOrder_MatchesExistingLimitOrder
// 驗證市價單能即時吃掉已掛的限價賣單，且多鎖定的 5% 緩衝資金正確退回
func TestE2E_MarketOrder_MatchesExistingLimitOrder(t *testing.T) {
	repo := setupE2EDB(t)
	svc := setupE2EService(repo)
	ctx := context.Background()

	// 賣家：1 BTC / $0 USD
	seller := createE2EUser(t, repo, decimal.NewFromInt(1), decimal.Zero)
	// 買家：$60,000 USD（市價買 1 BTC 需鎖定 $50,000×1.05=$52,500）
	buyer := createE2EUser(t, repo, decimal.Zero, decimal.NewFromInt(60000))

	// 賣家先入場掛限價賣單（Maker）
	sellOrder := makeLimitOrderRequest(seller.User.ID, core.SideSell, "50000", "1")
	require.NoError(t, svc.PlaceOrder(ctx, sellOrder))

	// 買家下市價買單（Taker），由引擎即時撮合
	marketBuyOrder := &core.Order{
		UserID:   buyer.User.ID,
		Symbol:   "BTC-USD",
		Side:     core.SideBuy,
		Type:     core.TypeMarket,
		Price:    decimal.Zero,
		Quantity: decimal.NewFromInt(1),
	}
	require.NoError(t, svc.PlaceOrder(ctx, marketBuyOrder))

	// 市價買單應完全成交
	fetchedMarket, err := repo.GetOrder(ctx, marketBuyOrder.ID)
	require.NoError(t, err)
	require.NotNil(t, fetchedMarket, "市價買單應已持久化至資料庫")
	assert.Equal(t, core.StatusFilled, fetchedMarket.Status, "市價買單應為 FILLED")

	// 限價賣單應完全成交
	fetchedSell, err := repo.GetOrder(ctx, sellOrder.ID)
	require.NoError(t, err)
	assert.Equal(t, core.StatusFilled, fetchedSell.Status, "限價賣單應為 FILLED")

	// 買家：應收到 1 BTC，USD 僅扣除實際成交金額（$50,000），緩衝退回
	// 計算：60000 - 50000 = 10000
	buyerBTC, err := repo.GetAccount(ctx, buyer.User.ID, "BTC")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(1).Equal(buyerBTC.Balance), "買家應收到 1 BTC")

	buyerUSD, err := repo.GetAccount(ctx, buyer.User.ID, "USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(10000).Equal(buyerUSD.Balance), "買家 USD 應為 10000 (60000-50000，5% 緩衝已退回)")
	assert.True(t, decimal.Zero.Equal(buyerUSD.Locked), "買家 USD 鎖定應為 0")

	// 賣家應收到成交金額 $50,000
	sellerUSD, err := repo.GetAccount(ctx, seller.User.ID, "USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(50000).Equal(sellerUSD.Balance), "賣家應收到 $50,000")
}

// TestE2E_CancelOrder_FundsReturnedToAvailable
// 驗證取消未成交的限價掛單後，鎖定的資金正確解鎖回可用餘額
func TestE2E_CancelOrder_FundsReturnedToAvailable(t *testing.T) {
	repo := setupE2EDB(t)
	svc := setupE2EService(repo)
	ctx := context.Background()

	// 買家：$55,000 USD，掛限價買單 @ $49,000（無對手賣單，不會成交）
	buyer := createE2EUser(t, repo, decimal.Zero, decimal.NewFromInt(55000))

	buyOrder := makeLimitOrderRequest(buyer.User.ID, core.SideBuy, "49000", "1")
	require.NoError(t, svc.PlaceOrder(ctx, buyOrder))

	// 下單後，$49,000 應被鎖定，可用餘額應為 $6,000
	usdAfterOrder, err := repo.GetAccount(ctx, buyer.User.ID, "USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(6000).Equal(usdAfterOrder.Balance), "下單後可用 USD 應為 6000 (55000-49000)")
	assert.True(t, decimal.NewFromInt(49000).Equal(usdAfterOrder.Locked), "下單後 USD 鎖定應為 49000")

	// 取消該訂單
	require.NoError(t, svc.CancelOrder(ctx, buyOrder.ID, buyer.User.ID))

	// 訂單狀態應更新為 CANCELED
	fetchedOrder, err := repo.GetOrder(ctx, buyOrder.ID)
	require.NoError(t, err)
	assert.Equal(t, core.StatusCanceled, fetchedOrder.Status, "訂單應為 CANCELED")

	// 資金應全部解鎖，餘額回復到初始值
	usdAfterCancel, err := repo.GetAccount(ctx, buyer.User.ID, "USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(55000).Equal(usdAfterCancel.Balance), "取消後 USD 餘額應回到 55000")
	assert.True(t, decimal.Zero.Equal(usdAfterCancel.Locked), "取消後 USD 鎖定應為 0")
}

// TestE2E_RestoreEngineSnapshot_RebuildFromDB
// 驗證模擬服務重啟後，RestoreEngineSnapshot 能正確從 DB 重建記憶體撮合引擎
// 測試流程：
//  1. 第一個 service 實例掛入兩筆不成交的訂單，驗證引擎狀態
//  2. 建立第二個 service 實例（模擬重啟），確認引擎為空
//  3. 呼叫 RestoreEngineSnapshot，驗證訂單簿與重啟前一致
//
// 使用 "E2E-USD" 隔離交易對，避免受 DB 中已有的 BTC-USD 模擬訂單影響
func TestE2E_RestoreEngineSnapshot_RebuildFromDB(t *testing.T) {
	repo := setupE2EDB(t)
	ctx := context.Background()

	// 使用隔離交易對，確保引擎在 testSymbol 下絕對乾淨
	const testSymbol = "E2E-USD"

	// 第一個 service 實例（模擬首次啟動）
	svc1 := core.NewExchangeService(repo, repo, repo, repo, repo, testSymbol, nil)

	// 掛入兩筆價格不相交的訂單（買 $49,000 vs 賣 $51,000，不會互相撮合）
	// testSymbol "E2E-USD"：base="E2E"，quote="USD"
	// 賣家需 "E2E" 帳戶（鎖定 base 貨幣）；買家需 "USD" 帳戶（鎖定 quote 貨幣）
	seller := createE2EUser(t, repo, decimal.Zero, decimal.Zero) // BTC/USD=0，後續補建 E2E
	buyer := createE2EUser(t, repo, decimal.Zero, decimal.NewFromInt(55000))  // USD=$55,000

	// 為賣家補建 "E2E" 帳戶（testSymbol 的 base 貨幣，供鎖定賣單使用）
	now := time.Now().UnixMilli()
	e2eSellerAcctID, _ := uuid.NewV7()
	require.NoError(t, repo.CreateAccount(ctx, &core.Account{
		ID:        e2eSellerAcctID,
		UserID:    seller.User.ID,
		Currency:  "E2E",
		Balance:   decimal.NewFromInt(1),
		Locked:    decimal.Zero,
		CreatedAt: now,
		UpdatedAt: now,
	}))
	// 為買家補建 "E2E" 帳戶（結算後接收 base 貨幣用，即使初始為 0）
	e2eBuyerAcctID, _ := uuid.NewV7()
	require.NoError(t, repo.CreateAccount(ctx, &core.Account{
		ID:        e2eBuyerAcctID,
		UserID:    buyer.User.ID,
		Currency:  "E2E",
		Balance:   decimal.Zero,
		Locked:    decimal.Zero,
		CreatedAt: now,
		UpdatedAt: now,
	}))

	sellOrder := &core.Order{
		UserID:   seller.User.ID,
		Symbol:   testSymbol,
		Side:     core.SideSell,
		Type:     core.TypeLimit,
		Price:    decimal.NewFromInt(51000),
		Quantity: decimal.NewFromInt(1),
	}
	require.NoError(t, svc1.PlaceOrder(ctx, sellOrder))

	buyOrder := &core.Order{
		UserID:   buyer.User.ID,
		Symbol:   testSymbol,
		Side:     core.SideBuy,
		Type:     core.TypeLimit,
		Price:    decimal.NewFromInt(49000),
		Quantity: decimal.NewFromInt(1),
	}
	require.NoError(t, svc1.PlaceOrder(ctx, buyOrder))

	// 驗證第一個 service 實例的訂單簿狀態
	snapshot1, err := svc1.GetOrderBook(ctx, testSymbol)
	require.NoError(t, err)
	assert.Len(t, snapshot1.Bids, 1, "首次啟動應有 1 筆掛買單")
	assert.Len(t, snapshot1.Asks, 1, "首次啟動應有 1 筆掛賣單")

	// === 模擬重啟：建立第二個 service 實例，記憶體引擎為空 ===
	svc2 := core.NewExchangeService(repo, repo, repo, repo, repo, testSymbol, nil)

	// 重啟後引擎應為空（未呼叫 RestoreEngineSnapshot 前）
	snapshot2Before, err := svc2.GetOrderBook(ctx, testSymbol)
	require.NoError(t, err)
	assert.Len(t, snapshot2Before.Bids, 0, "重啟後引擎應無掛買單（快照尚未恢復）")
	assert.Len(t, snapshot2Before.Asks, 0, "重啟後引擎應無掛賣單（快照尚未恢復）")

	// 執行快照恢復（從 DB 重建所有活動訂單至記憶體引擎）
	require.NoError(t, svc2.RestoreEngineSnapshot(ctx))

	// 恢復後，testSymbol 引擎應精確包含我們的 2 筆訂單（E2E-USD 為隔離交易對）
	snapshot2After, err := svc2.GetOrderBook(ctx, testSymbol)
	require.NoError(t, err)
	assert.Len(t, snapshot2After.Bids, 1, "恢復後應有 1 筆掛買單")
	assert.Len(t, snapshot2After.Asks, 1, "恢復後應有 1 筆掛賣單")

	// 驗證恢復的訂單價格正確
	if len(snapshot2After.Bids) > 0 {
		assert.True(t, decimal.NewFromInt(49000).Equal(snapshot2After.Bids[0].Price), "掛買單價格應恢復為 49000")
	}
	if len(snapshot2After.Asks) > 0 {
		assert.True(t, decimal.NewFromInt(51000).Equal(snapshot2After.Asks[0].Price), "掛賣單價格應恢復為 51000")
	}
}
