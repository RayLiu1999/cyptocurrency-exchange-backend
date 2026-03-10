//go:build integration

package core_test

import (
	"context"
	"sync"
	"testing"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Phase 6: 高併發與競態條件測試
// 驗證多用戶同時下單不會導致雙倍支出（Double Spend）或資金不一致
// =============================================================================

// TestConcurrency_MultipleUsersCancelSameOrder_OnlyOneSucceeds
// 競態條件場景：10 個 goroutine 同時嘗試取消同一筆訂單
// 驗證重點：
//  1. 只有一個 CancelOrder 能成功，其餘應返回錯誤
//  2. 資金只被解鎖一次，不會重複退回
func TestConcurrency_MultipleUsersCancelSameOrder_OnlyOneSucceeds(t *testing.T) {
	repo := setupE2EDB(t)
	svc := setupE2EService(repo)
	ctx := context.Background()

	// 建立用戶：持有 $100,000 USD
	buyer := createE2EUser(t, repo, decimal.Zero, decimal.NewFromInt(100000))

	// 掛一筆不會成交的限價買單（$30,000，低於市價）
	buyOrder := makeLimitOrderRequest(buyer.User.ID, core.SideBuy, "30000", "1")
	require.NoError(t, svc.PlaceOrder(ctx, buyOrder))

	// 確認下單後資金已鎖定
	accBefore, err := repo.GetAccount(ctx, buyer.User.ID, "USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(30000).Equal(accBefore.Locked), "下單後應鎖定 $30,000")

	// 10 個 goroutine 同時取消同一筆訂單
	const workers = 10
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			err := svc.CancelOrder(ctx, buyOrder.ID, buyer.User.ID)
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// 只能有一個成功
	assert.Equal(t, 1, successCount, "10 個並發取消中，只能有 1 個成功")

	// 訂單狀態應為 CANCELED
	fetchedOrder, err := repo.GetOrder(ctx, buyOrder.ID)
	require.NoError(t, err)
	assert.Equal(t, core.StatusCanceled, fetchedOrder.Status, "訂單最終狀態應為 CANCELED")

	// 資金只解鎖一次，餘額回到初始值
	accAfter, err := repo.GetAccount(ctx, buyer.User.ID, "USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(100000).Equal(accAfter.Balance), "取消後 USD 餘額應為 100000（只解鎖一次）")
	assert.True(t, decimal.Zero.Equal(accAfter.Locked), "取消後 USD 鎖定應為 0")
}

// TestConcurrency_SimultaneousOrders_NoDoubleLock
// 雙倍鎖定防護：同一用戶使用 $50,000 USD 同時送出兩筆各需 $50,000 的限價買單
// 驗證重點：
//  1. 只有一筆訂單能成功（餘額不足）
//  2. 帳戶餘額不會變成負數
func TestConcurrency_SimultaneousOrders_NoDoubleLock(t *testing.T) {
	repo := setupE2EDB(t)
	svc := setupE2EService(repo)
	ctx := context.Background()

	// 用戶只有 $50,000 USD，剛好夠一筆 1 BTC @ $50,000 的買單
	buyer := createE2EUser(t, repo, decimal.Zero, decimal.NewFromInt(50000))

	const workers = 5
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			// 每個 goroutine 都嘗試下相同金額的買單（各需鎖定 $50,000）
			order := makeLimitOrderRequest(buyer.User.ID, core.SideBuy, "50000", "1")
			err := svc.PlaceOrder(ctx, order)
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// 只能有 1 筆下單成功（餘額只夠一筆）
	assert.Equal(t, 1, successCount, "5 個並發下單中，因餘額只夠一筆，只應有 1 個成功")

	// 帳戶餘額不應為負數
	accAfter, err := repo.GetAccount(ctx, buyer.User.ID, "USD")
	require.NoError(t, err)
	assert.True(t, accAfter.Balance.GreaterThanOrEqual(decimal.Zero), "帳戶 USD 可用餘額不應為負數")
	assert.True(t, accAfter.Locked.GreaterThanOrEqual(decimal.Zero), "帳戶 USD 鎖定不應為負數")

	// 總資金守恆：balance + locked 必須等於初始 $50,000
	totalFunds := accAfter.Balance.Add(accAfter.Locked)
	assert.True(t, decimal.NewFromInt(50000).Equal(totalFunds), "資金守恆：balance + locked 應等於初始 50000")
}

// TestConcurrency_MultiSeller_SingleBuyer_FundsConserved
// 多對一撮合：5 個賣家各持有 1 BTC，1 個買家同時嘗試買 5 BTC
// 驗證重點：
//  1. 所有成交均正確記錄，無重複結算
//  2. 買家 USD 總支出 = 賣家 USD 總收入（資金守恆）
//  3. 買家 BTC 增加量 = 賣家 BTC 減少量
func TestConcurrency_MultiSeller_SingleBuyer_FundsConserved(t *testing.T) {
	repo := setupE2EDB(t)
	svc := setupE2EService(repo)
	ctx := context.Background()

	const sellerCount = 5
	const pricePerBTC = int64(50000)

	// 買家：持有足夠買下 5 BTC 的 USD
	buyer := createE2EUser(t, repo, decimal.Zero, decimal.NewFromInt(pricePerBTC*sellerCount+10000))

	// 建立 5 個賣家，各持有 1 BTC
	sellers := make([]*e2eUser, sellerCount)
	for i := 0; i < sellerCount; i++ {
		sellers[i] = createE2EUser(t, repo, decimal.NewFromInt(1), decimal.Zero)
	}

	// 賣家同時掛賣單（Maker）
	var wg sync.WaitGroup
	wg.Add(sellerCount)
	for i := 0; i < sellerCount; i++ {
		s := sellers[i]
		go func() {
			defer wg.Done()
			order := makeLimitOrderRequest(s.User.ID, core.SideSell, "50000", "1")
			require.NoError(t, svc.PlaceOrder(ctx, order))
		}()
	}
	wg.Wait()

	// 買家一次吃 5 BTC（Taker）
	buyOrder := makeLimitOrderRequest(buyer.User.ID, core.SideBuy, "50000", "5")
	require.NoError(t, svc.PlaceOrder(ctx, buyOrder))

	// === 驗證資金守恆 ===
	// 買家：BTC 應增加 5（可能因成交順序不同而分批到達）
	buyerBTC, err := repo.GetAccount(ctx, buyer.User.ID, "BTC")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(5).Equal(buyerBTC.Balance), "買家應持有 5 BTC")

	// 買家：USD 應減少 5 × $50,000 = $250,000
	buyerUSD, err := repo.GetAccount(ctx, buyer.User.ID, "USD")
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(10000).Equal(buyerUSD.Balance), "買家 USD 剩餘應為 10000 (260000-250000)")
	assert.True(t, decimal.Zero.Equal(buyerUSD.Locked), "買家 USD 鎖定應為 0")

	// 賣家：每人各收到 $50,000 USD，BTC 歸零
	totalSellerUSD := decimal.Zero
	for _, s := range sellers {
		sellerUSD, err := repo.GetAccount(ctx, s.User.ID, "USD")
		require.NoError(t, err)
		totalSellerUSD = totalSellerUSD.Add(sellerUSD.Balance)

		sellerBTC, err := repo.GetAccount(ctx, s.User.ID, "BTC")
		require.NoError(t, err)
		assert.True(t, decimal.Zero.Equal(sellerBTC.Balance), "賣家 BTC 應已全部賣出")
		assert.True(t, decimal.Zero.Equal(sellerBTC.Locked), "賣家 BTC 鎖定應為 0")
	}
	assert.True(t, decimal.NewFromInt(pricePerBTC*sellerCount).Equal(totalSellerUSD),
		"所有賣家 USD 總收入應等於 250000")
}

// TestConcurrency_Race_PlaceAndCancel_NoNegativeBalance
// 競態下單與取消：兩個 goroutine 各自連續下單並立刻取消
// 使用 -race 旗標可配合此測試驗證 race condition
// 驗證重點：整個過程中帳戶餘額絕不為負數，最終資金守恆
func TestConcurrency_Race_PlaceAndCancel_NoNegativeBalance(t *testing.T) {
	repo := setupE2EDB(t)
	svc := setupE2EService(repo)
	ctx := context.Background()

	const initialUSD = int64(100000)
	buyer := createE2EUser(t, repo, decimal.Zero, decimal.NewFromInt(initialUSD))

	// 2 個 goroutine 各自連續下 5 筆掛單並立刻取消（總計 10 次 place + 10 次 cancel）
	const goroutines = 2
	const iterations = 5
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				order := makeLimitOrderRequest(buyer.User.ID, core.SideBuy, "30000", "1")
				if err := svc.PlaceOrder(ctx, order); err != nil {
					continue // 餘額不足時跳過
				}
				// 立刻取消（允許因已被撮合而取消失敗）
				_ = svc.CancelOrder(ctx, order.ID, buyer.User.ID)
			}
		}()
	}
	wg.Wait()

	// 最終帳戶狀態驗證
	accFinal, err := repo.GetAccount(ctx, buyer.User.ID, "USD")
	require.NoError(t, err)

	// 餘額與鎖定均不為負
	assert.True(t, accFinal.Balance.GreaterThanOrEqual(decimal.Zero), "最終 USD 可用餘額不應為負數")
	assert.True(t, accFinal.Locked.GreaterThanOrEqual(decimal.Zero), "最終 USD 鎖定不應為負數")

	// 資金守恆：balance + locked <= initialUSD（取消訂單後資金應回歸，已成交部分 <= 初始資金）
	total := accFinal.Balance.Add(accFinal.Locked)
	assert.True(t, total.LessThanOrEqual(decimal.NewFromInt(initialUSD)),
		"最終 balance + locked 不應超過初始資金 100000")
}
