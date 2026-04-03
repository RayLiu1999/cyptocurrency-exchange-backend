//go:build integration

package repository_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"github.com/RayLiu1999/exchange/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *repository.PostgresRepository {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:123qwe@localhost:5432/exchange?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skipf("skip: cannot create pool (%v)", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("skip: db unreachable (%v)", err)
	}
	t.Cleanup(func() { pool.Close() })
	return repository.NewPostgresRepository(pool)
}

func createTestUser(t *testing.T, repo *repository.PostgresRepository) *domain.User {
	t.Helper()
	ctx := context.Background()
	userID, err := uuid.NewV7()
	require.NoError(t, err)
	now := time.Now().UnixMilli()
	user := &domain.User{
		ID:           userID,
		Email:        fmt.Sprintf("int-test-%s@test.local", userID),
		PasswordHash: "hash_not_used",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	require.NoError(t, repo.CreateUser(ctx, user))
	t.Cleanup(func() { cleanupUser(context.Background(), repo, userID) })
	return user
}

func cleanupUser(ctx context.Context, repo *repository.PostgresRepository, userID uuid.UUID) {
	pool := repo.Pool()
	pool.Exec(ctx, `DELETE FROM trades WHERE maker_order_id IN (SELECT id FROM orders WHERE user_id = $1) OR taker_order_id IN (SELECT id FROM orders WHERE user_id = $1)`, userID)
	pool.Exec(ctx, `DELETE FROM orders WHERE user_id = $1`, userID)
	pool.Exec(ctx, `DELETE FROM accounts WHERE user_id = $1`, userID)
	pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
}

func makeLimitOrder(userID uuid.UUID, side domain.OrderSide, price string) *domain.Order {
	orderID, _ := uuid.NewV7()
	now := time.Now().UnixMilli()
	return &domain.Order{
		ID:             orderID,
		UserID:         userID,
		Symbol:         "BTC-USD",
		Side:           side,
		Type:           domain.TypeLimit,
		Price:          decimal.RequireFromString(price),
		Quantity:       decimal.NewFromInt(1),
		FilledQuantity: decimal.Zero,
		Status:         domain.StatusNew,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestAccountRepository_CreateAndGet(t *testing.T) {
	repo := setupTestDB(t)
	user := createTestUser(t, repo)
	ctx := context.Background()
	accountID, _ := uuid.NewV7()
	now := time.Now().UnixMilli()
	acc := &domain.Account{
		ID: accountID, UserID: user.ID, Currency: "USD",
		Balance: decimal.NewFromInt(10000), Locked: decimal.Zero,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateAccount(ctx, acc))
	fetched, err := repo.GetAccount(ctx, user.ID, "USD")
	require.NoError(t, err)
	assert.Equal(t, user.ID, fetched.UserID)
	assert.True(t, decimal.NewFromInt(10000).Equal(fetched.Balance))
	assert.True(t, decimal.Zero.Equal(fetched.Locked))
}

func TestAccountRepository_CreateAccount_DuplicateIsIgnored(t *testing.T) {
	repo := setupTestDB(t)
	user := createTestUser(t, repo)
	ctx := context.Background()
	accountID, _ := uuid.NewV7()
	now := time.Now().UnixMilli()
	acc := &domain.Account{
		ID: accountID, UserID: user.ID, Currency: "BTC",
		Balance: decimal.NewFromInt(5), Locked: decimal.Zero,
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateAccount(ctx, acc))
	assert.NoError(t, repo.CreateAccount(ctx, acc))
}

func TestAccountRepository_LockAndUnlockFunds(t *testing.T) {
	repo := setupTestDB(t)
	user := createTestUser(t, repo)
	ctx := context.Background()
	accountID, _ := uuid.NewV7()
	now := time.Now().UnixMilli()
	require.NoError(t, repo.CreateAccount(ctx, &domain.Account{
		ID: accountID, UserID: user.ID, Currency: "USD",
		Balance: decimal.NewFromInt(100), Locked: decimal.Zero,
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, repo.LockFunds(ctx, user.ID, "USD", decimal.NewFromInt(60)))
	acc, _ := repo.GetAccount(ctx, user.ID, "USD")
	assert.True(t, decimal.NewFromInt(40).Equal(acc.Balance))
	assert.True(t, decimal.NewFromInt(60).Equal(acc.Locked))
	require.NoError(t, repo.UnlockFunds(ctx, user.ID, "USD", decimal.NewFromInt(60)))
	acc, _ = repo.GetAccount(ctx, user.ID, "USD")
	assert.True(t, decimal.NewFromInt(100).Equal(acc.Balance))
	assert.True(t, decimal.Zero.Equal(acc.Locked))
}

func TestAccountRepository_LockFunds_InsufficientFunds_ReturnsError(t *testing.T) {
	repo := setupTestDB(t)
	user := createTestUser(t, repo)
	ctx := context.Background()
	accountID, _ := uuid.NewV7()
	now := time.Now().UnixMilli()
	require.NoError(t, repo.CreateAccount(ctx, &domain.Account{
		ID: accountID, UserID: user.ID, Currency: "USD",
		Balance: decimal.NewFromInt(10), Locked: decimal.Zero,
		CreatedAt: now, UpdatedAt: now,
	}))
	err := repo.LockFunds(ctx, user.ID, "USD", decimal.NewFromInt(100))
	assert.ErrorIs(t, err, core.ErrInsufficientFunds)
}

func TestAccountRepository_GetAccountsByUser(t *testing.T) {
	repo := setupTestDB(t)
	user := createTestUser(t, repo)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	for _, currency := range []string{"BTC", "USD"} {
		id, _ := uuid.NewV7()
		require.NoError(t, repo.CreateAccount(ctx, &domain.Account{
			ID: id, UserID: user.ID, Currency: currency,
			Balance: decimal.NewFromInt(1), Locked: decimal.Zero,
			CreatedAt: now, UpdatedAt: now,
		}))
	}
	accounts, err := repo.GetAccountsByUser(ctx, user.ID)
	require.NoError(t, err)
	assert.Len(t, accounts, 2)
}

func TestOrderRepository_CreateAndGet(t *testing.T) {
	repo := setupTestDB(t)
	user := createTestUser(t, repo)
	ctx := context.Background()
	order := makeLimitOrder(user.ID, domain.SideBuy, "50000")
	require.NoError(t, repo.CreateOrder(ctx, order))
	fetched, err := repo.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, order.ID, fetched.ID)
	assert.Equal(t, user.ID, fetched.UserID)
	assert.Equal(t, domain.SideBuy, fetched.Side)
	assert.True(t, order.Price.Equal(fetched.Price))
	assert.Equal(t, domain.StatusNew, fetched.Status)
}

func TestOrderRepository_UpdateOrder(t *testing.T) {
	repo := setupTestDB(t)
	user := createTestUser(t, repo)
	ctx := context.Background()
	order := makeLimitOrder(user.ID, domain.SideSell, "51000")
	require.NoError(t, repo.CreateOrder(ctx, order))
	order.FilledQuantity = decimal.NewFromFloat(0.5)
	order.Status = domain.StatusPartiallyFilled
	order.UpdatedAt = time.Now().UnixMilli()
	require.NoError(t, repo.UpdateOrder(ctx, order))
	fetched, _ := repo.GetOrder(ctx, order.ID)
	assert.True(t, decimal.NewFromFloat(0.5).Equal(fetched.FilledQuantity))
	assert.Equal(t, domain.StatusPartiallyFilled, fetched.Status)
}

func TestOrderRepository_GetOrdersByUser(t *testing.T) {
	repo := setupTestDB(t)
	user := createTestUser(t, repo)
	ctx := context.Background()
	for i := range 3 {
		o := makeLimitOrder(user.ID, domain.SideBuy, fmt.Sprintf("%d", 49000+i*1000))
		require.NoError(t, repo.CreateOrder(ctx, o))
	}
	orders, err := repo.GetOrdersByUser(ctx, user.ID)
	require.NoError(t, err)
	assert.Len(t, orders, 3)
}

func TestOrderRepository_GetActiveOrders_OnlyNewAndPartial(t *testing.T) {
	repo := setupTestDB(t)
	user := createTestUser(t, repo)
	ctx := context.Background()
	newOrder := makeLimitOrder(user.ID, domain.SideBuy, "50000")
	require.NoError(t, repo.CreateOrder(ctx, newOrder))
	partialOrder := makeLimitOrder(user.ID, domain.SideSell, "51000")
	partialOrder.Status = domain.StatusPartiallyFilled
	partialOrder.FilledQuantity = decimal.NewFromFloat(0.3)
	require.NoError(t, repo.CreateOrder(ctx, partialOrder))
	filledOrder := makeLimitOrder(user.ID, domain.SideBuy, "49000")
	filledOrder.Status = domain.StatusFilled
	filledOrder.FilledQuantity = decimal.NewFromInt(1)
	require.NoError(t, repo.CreateOrder(ctx, filledOrder))
	activeOrders, err := repo.GetActiveOrders(ctx)
	require.NoError(t, err)
	activeIDs := make(map[uuid.UUID]bool)
	for _, o := range activeOrders {
		activeIDs[o.ID] = true
	}
	assert.True(t, activeIDs[newOrder.ID])
	assert.True(t, activeIDs[partialOrder.ID])
	assert.False(t, activeIDs[filledOrder.ID])
}

func TestTradeRepository_CreateTrade(t *testing.T) {
	repo := setupTestDB(t)
	buyer := createTestUser(t, repo)
	seller := createTestUser(t, repo)
	ctx := context.Background()
	buyOrder := makeLimitOrder(buyer.ID, domain.SideBuy, "50000")
	sellOrder := makeLimitOrder(seller.ID, domain.SideSell, "50000")
	require.NoError(t, repo.CreateOrder(ctx, buyOrder))
	require.NoError(t, repo.CreateOrder(ctx, sellOrder))
	tradeID, _ := uuid.NewV7()
	trade := &engine.Trade{
		ID:           tradeID,
		Symbol:       "BTC-USD",
		MakerOrderID: sellOrder.ID,
		TakerOrderID: buyOrder.ID,
		Price:        decimal.NewFromInt(50000),
		Quantity:     decimal.NewFromInt(1),
		CreatedAt:    time.Now().UnixMilli(),
	}
	assert.NoError(t, repo.CreateTrade(ctx, trade))
}

func TestTradeRepository_GetRecentTrades(t *testing.T) {
	repo := setupTestDB(t)
	buyer := createTestUser(t, repo)
	seller := createTestUser(t, repo)
	ctx := context.Background()
	buyOrder := makeLimitOrder(buyer.ID, domain.SideBuy, "50000")
	sellOrder := makeLimitOrder(seller.ID, domain.SideSell, "50000")
	require.NoError(t, repo.CreateOrder(ctx, buyOrder))
	require.NoError(t, repo.CreateOrder(ctx, sellOrder))
	tradeID, _ := uuid.NewV7()
	trade := &engine.Trade{
		ID:           tradeID,
		Symbol:       "BTC-USD",
		MakerOrderID: sellOrder.ID,
		TakerOrderID: buyOrder.ID,
		Price:        decimal.NewFromInt(50000),
		Quantity:     decimal.NewFromInt(1),
		CreatedAt:    time.Now().UnixMilli(),
	}
	require.NoError(t, repo.CreateTrade(ctx, trade))
	trades, err := repo.GetRecentTrades(ctx, "BTC-USD", 10)
	require.NoError(t, err)
	ids := make(map[uuid.UUID]bool)
	for _, tr := range trades {
		ids[tr.ID] = true
	}
	assert.True(t, ids[tradeID])
}

func TestExecTx_Success_AllStepsCommitted(t *testing.T) {
	repo := setupTestDB(t)
	user := createTestUser(t, repo)
	ctx := context.Background()
	var createdOrderID uuid.UUID
	err := repo.ExecTx(ctx, func(txCtx context.Context) error {
		accountID, _ := uuid.NewV7()
		now := time.Now().UnixMilli()
		if err := repo.CreateAccount(txCtx, &domain.Account{
			ID: accountID, UserID: user.ID, Currency: "USD",
			Balance: decimal.NewFromInt(5000), Locked: decimal.Zero,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := repo.LockFunds(txCtx, user.ID, "USD", decimal.NewFromInt(5000)); err != nil {
			return err
		}
		order := makeLimitOrder(user.ID, domain.SideBuy, "50000")
		createdOrderID = order.ID
		return repo.CreateOrder(txCtx, order)
	})
	require.NoError(t, err)
	acc, _ := repo.GetAccount(ctx, user.ID, "USD")
	assert.True(t, decimal.Zero.Equal(acc.Balance))
	assert.True(t, decimal.NewFromInt(5000).Equal(acc.Locked))
	order, _ := repo.GetOrder(ctx, createdOrderID)
	assert.NotNil(t, order)
}

func TestExecTx_Failure_ChangesRolledBack(t *testing.T) {
	repo := setupTestDB(t)
	user := createTestUser(t, repo)
	ctx := context.Background()
	var rolledBackOrderID uuid.UUID
	err := repo.ExecTx(ctx, func(txCtx context.Context) error {
		accountID, _ := uuid.NewV7()
		now := time.Now().UnixMilli()
		if err := repo.CreateAccount(txCtx, &domain.Account{
			ID: accountID, UserID: user.ID, Currency: "BTC",
			Balance: decimal.NewFromInt(2), Locked: decimal.Zero,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		order := makeLimitOrder(user.ID, domain.SideSell, "51000")
		rolledBackOrderID = order.ID
		if err := repo.CreateOrder(txCtx, order); err != nil {
			return err
		}
		return fmt.Errorf("simulated failure")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated failure")
	acc, _ := repo.GetAccount(ctx, user.ID, "BTC")
	assert.Nil(t, acc)
	order, _ := repo.GetOrder(ctx, rolledBackOrderID)
	assert.Nil(t, order)
}
