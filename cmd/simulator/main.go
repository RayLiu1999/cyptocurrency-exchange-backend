package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// Config
var (
	dbURL      string
	apiURL     string
	symbol     = "ETH-USD"
	basePrice  = 3000.0
	numTraders = 20 // 10 buyers, 10 sellers
	totalTx    = 10000
)

func init() {
	flag.StringVar(&dbURL, "db", "postgres://postgres:123qwe@localhost:5432/exchange?sslmode=disable", "Database URL")
	flag.StringVar(&apiURL, "api", "http://localhost:8080/api/v1", "API URL")
	flag.IntVar(&totalTx, "tx", 10000, "Total transactions to simulate")
	flag.Parse()

	if envURL := os.Getenv("DATABASE_URL"); envURL != "" {
		dbURL = envURL
	}
}

type Trader struct {
	ID    uuid.UUID
	Role  string // "MAKER" or "TAKER"
	Token string // Simulate auth token (userID for now in header)
}

func main() {
	log.Println("🚀 Starting Market Simulator...")
	log.Printf("Target: %d Transactions | Symbol: %s | BasePrice: %.2f", totalTx, symbol, basePrice)

	// 0. Setup Signal Handling
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// 1. Connect DB
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close(context.Background()) // use background context for cleanup

	// 2. Initialize Traders (Seed DB)
	traders := seedTraders(ctx, conn)
	log.Printf("✅ Seeded %d traders with funds", len(traders))

	// 3. Start Simulation
	log.Println("🔥 Firing orders... Press Ctrl+C to stop.")
	runSimulation(ctx, traders)

	// 4. Verify Results (Stats)
	// Use background context for printing stats as main ctx might be cancelled
	printStats(context.Background(), conn)
}

func seedTraders(ctx context.Context, conn *pgx.Conn) []Trader {
	var traders []Trader
	for i := 0; i < numTraders; i++ {
		uid := uuid.New()
		email := fmt.Sprintf("bot_%d_%s@sim.com", i, uid.String()[:8])

		// Create User
		_, err := conn.Exec(ctx, "INSERT INTO users (id, email, password_hash, created_at, updated_at) VALUES ($1, $2, $3, NOW(), NOW())", uid, email, "hash")
		if err != nil {
			log.Printf("Failed to create user %s: %v", email, err)
			continue
		}

		// Deposit Funds (Both USD and ETH)
		// Give them a lot so they don't run out
		_, err = conn.Exec(ctx, "INSERT INTO accounts (id, user_id, currency, balance, locked, created_at, updated_at) VALUES ($1, $2, 'USD', 10000000, 0, NOW(), NOW())", uuid.New(), uid)
		if err != nil {
			log.Printf("Failed to deposit USD: %v", err)
		}
		_, err = conn.Exec(ctx, "INSERT INTO accounts (id, user_id, currency, balance, locked, created_at, updated_at) VALUES ($1, $2, 'ETH', 10000, 0, NOW(), NOW())", uuid.New(), uid)
		if err != nil {
			log.Printf("Failed to deposit ETH: %v", err)
		}

		role := "TAKER"
		if i < numTraders/2 {
			role = "MAKER"
		}

		traders = append(traders, Trader{ID: uid, Role: role})
	}
	return traders
}

func runSimulation(ctx context.Context, traders []Trader) {
	var wg sync.WaitGroup
	// Context with cancel for graceful shutdown handled by main?
	// actually main already provides ctx, but we need to listen to signal there.

	// Price Walker
	currentPrice := basePrice
	priceMu := sync.Mutex{}

	// Update price periodically (Random Walk)
	// 注意：不能在這裡 cancel 傳入的 ctx，否則會中斷所有 workers
	walkCtx, cancelWalk := context.WithCancel(context.Background())
	defer cancelWalk()

	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-walkCtx.Done():
				return
			case <-ticker.C:
				priceMu.Lock()
				// Volatility: +/- 0.5%
				change := (rand.Float64() - 0.5) * 0.01 * currentPrice
				currentPrice += change
				priceMu.Unlock()
			}
		}
	}()

	// Workers
	start := time.Now()

	// Launch parallel workers
	workerCount := 10
	ordersPerWorker := totalTx / workerCount

	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))

			for i := 0; i < ordersPerWorker; i++ {
				// Check for cancellation
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Pick random trader
				trader := traders[r.Intn(len(traders))]

				priceMu.Lock()
				refPrice := currentPrice
				priceMu.Unlock()

				// Determine Side
				side := "BUY"
				if r.Intn(2) == 0 {
					side = "SELL"
				}

				// Determine Type & Price
				// Makers place Limit orders near price
				// Takers place Market orders (or Limit crossing price)

				orderType := "LIMIT"
				price := refPrice

				if trader.Role == "TAKER" {
					// Aggressive Taker
					if r.Float64() < 0.8 {
						orderType = "MARKET"
						price = 0 // Market order ignores price
					} else {
						// Limit Crossing
						if side == "BUY" {
							price = refPrice * 1.02
						} else {
							price = refPrice * 0.98
						}
					}
				} else {
					// Passive Maker
					spread := r.Float64() * 0.02 * refPrice // 0-2% spread
					if side == "BUY" {
						price = refPrice - spread
					} else {
						price = refPrice + spread
					}
				}

				qty := decimal.NewFromFloat(0.1 + r.Float64()) // 0.1 ~ 1.1 ETH

				// API Call
				placeOrder(trader, side, orderType, price, qty)
			}
		}(w)
	}

	wg.Wait()
	duration := time.Since(start)
	log.Printf("🏁 Simulation Completed! %d txs in %v (%.2f TPS)", totalTx, duration, float64(totalTx)/duration.Seconds())
}

func printStats(ctx context.Context, conn *pgx.Conn) {
	log.Println("\n📊 Verifying Persistence Logs...")

	// Count Orders
	var orderCount int
	err := conn.QueryRow(ctx, "SELECT COUNT(*) FROM orders").Scan(&orderCount)
	if err != nil {
		log.Printf("Failed to count orders: %v", err)
	}

	// Count Trades
	var tradeCount int
	err = conn.QueryRow(ctx, "SELECT COUNT(*) FROM trades").Scan(&tradeCount)
	if err != nil {
		log.Printf("Failed to count trades: %v", err)
	}

	log.Println("---------------------------------------------------")
	log.Printf("Total Orders in DB: %d", orderCount)
	log.Printf("Total Trades in DB: %d", tradeCount)
	log.Println("---------------------------------------------------")

	if tradeCount == 0 && orderCount > 0 {
		log.Println("❌ WARNING: Trades table is empty! Persistence might be failing.")
	} else if tradeCount > 0 {
		log.Println("✅ Trades are being persisted correctly.")
	}
}

type OrderRequest struct {
	UserID   string          `json:"user_id"`
	Symbol   string          `json:"symbol"`
	Side     string          `json:"side"`
	Type     string          `json:"type"`
	Price    decimal.Decimal `json:"price"`
	Quantity decimal.Decimal `json:"quantity"`
}

func placeOrder(t Trader, side, oType string, price float64, qty decimal.Decimal) {
	req := OrderRequest{
		UserID:   t.ID.String(),
		Symbol:   symbol,
		Side:     side,
		Type:     oType,
		Price:    decimal.NewFromFloat(price),
		Quantity: qty,
	}

	body, _ := json.Marshal(req)

	httpReq, _ := http.NewRequest("POST", apiURL+"/orders", bytes.NewBuffer(body))
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("❌ 訂單提交失敗: %v", err)
		return
	}
	defer resp.Body.Close()

	// Check status
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		var respBody map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respBody)
		log.Printf("❌ 訂單被拒絕 [%d]: %v", resp.StatusCode, respBody)
	}
}
