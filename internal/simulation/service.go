package simulation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Config 模擬器啟動配置
type Config struct {
	Symbol      string  `json:"symbol"`
	BasePrice   float64 `json:"base_price"`
	NumBots     int     `json:"num_bots"`
	TotalTx     int     `json:"total_tx"`
	WorkerCount int     `json:"worker_count"`
	Infinite    bool    `json:"infinite"`
	IntervalMs  int     `json:"interval_ms"`
}

// Status 模擬器目前狀態
type Status struct {
	Running    bool       `json:"running"`
	Symbol     string     `json:"symbol"`
	TotalTx    int        `json:"total_tx"`
	Infinite   bool       `json:"infinite"`
	SentTx     int64      `json:"sent_tx"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Message    string     `json:"message,omitempty"`
}

// bot 機器人身份
type bot struct {
	userID uuid.UUID
	role   string // "MAKER" or "TAKER"
}

// joinResponse 對應 POST /api/v1/test/join 的回應
type joinResponse struct {
	UserID uuid.UUID `json:"user_id"`
}

// placeOrderRequest 對應 POST /api/v1/orders 的請求
type placeOrderRequest struct {
	UserID   uuid.UUID       `json:"user_id"`
	Symbol   string          `json:"symbol"`
	Side     string          `json:"side"`
	Type     string          `json:"type"`
	Price    decimal.Decimal `json:"price"`
	Quantity decimal.Decimal `json:"quantity"`
}

// Service 模擬交易服務，透過 HTTP API 對 Gateway 發起壓力測試
type Service struct {
	gatewayURL string
	httpClient *http.Client
	mu         sync.Mutex
	running    bool
	cancelFunc context.CancelFunc
	status     Status
	sentTx     int64
}

// NewService 建立模擬服務，gatewayURL 例如 "http://gateway:8082"
func NewService(gatewayURL string) *Service {
	return &Service{
		gatewayURL: gatewayURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        200,
				MaxIdleConnsPerHost: 200,
			},
		},
	}
}

// Start 啟動模擬器（背景執行）
func (s *Service) Start(cfg Config) error {
	cfg = applyDefaults(cfg)

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("模擬器已在運行中")
	}
	s.running = true
	atomic.StoreInt64(&s.sentTx, 0)
	s.status = Status{
		Running:   true,
		Symbol:    cfg.Symbol,
		TotalTx:   cfg.TotalTx,
		Infinite:  cfg.Infinite,
		StartedAt: time.Now(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFunc = cancel
	s.mu.Unlock()

	go s.run(ctx, cfg)
	return nil
}

// Stop 停止模擬器
func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return errors.New("模擬器未啟動")
	}
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	return nil
}

// GetStatus 回傳目前狀態
func (s *Service) GetStatus() Status {
	s.mu.Lock()
	st := s.status
	running := s.running
	s.mu.Unlock()

	st.Running = running
	st.SentTx = atomic.LoadInt64(&s.sentTx)
	return st
}

func (s *Service) run(ctx context.Context, cfg Config) {
	logger.Info("模擬器啟動",
		zap.String("symbol", cfg.Symbol),
		zap.Int("num_bots", cfg.NumBots),
	)

	bots := s.spawnBots(ctx, cfg.NumBots)
	if len(bots) == 0 {
		s.finish("無法建立任何機器人，終止模擬")
		return
	}

	logger.Info("機器人初始化完成", zap.Int("count", len(bots)))
	s.runSimulation(ctx, bots, cfg)
	s.finish("模擬完成")
}

func (s *Service) finish(message string) {
	now := time.Now()
	s.mu.Lock()
	s.running = false
	s.cancelFunc = nil
	s.status.Running = false
	s.status.FinishedAt = &now
	s.status.Message = message
	s.mu.Unlock()
	logger.Info("模擬器已停止", zap.String("message", message))
}

// spawnBots 對 Gateway 呼叫 POST /api/v1/test/join 建立機器人
func (s *Service) spawnBots(ctx context.Context, numBots int) []bot {
	bots := make([]bot, 0, numBots)
	for i := 0; i < numBots; i++ {
		select {
		case <-ctx.Done():
			return bots
		default:
		}

		resp, err := s.httpClient.Post(
			s.gatewayURL+"/api/v1/test/join",
			"application/json",
			nil,
		)
		if err != nil {
			logger.Warn("機器人建立失敗", zap.Int("index", i), zap.Error(err))
			continue
		}

		var jr joinResponse
		if decErr := json.NewDecoder(resp.Body).Decode(&jr); decErr != nil {
			resp.Body.Close()
			logger.Warn("解析 join 回應失敗", zap.Error(decErr))
			continue
		}
		resp.Body.Close()

		role := "TAKER"
		if i < numBots/2 {
			role = "MAKER"
		}
		bots = append(bots, bot{userID: jr.UserID, role: role})
	}
	return bots
}

// runSimulation 啟動所有機器人 Goroutine 進行交易
func (s *Service) runSimulation(ctx context.Context, bots []bot, cfg Config) {
	currentPrice := cfg.BasePrice
	var priceMu sync.Mutex

	// 背景協程：以隨機步道 (Random Walk) 模型持續波動價格
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
				change := (rand.Float64() - 0.5) * 0.01 * currentPrice
				currentPrice += change
				priceMu.Unlock()
			}
		}
	}()

	workerCount := cfg.WorkerCount
	if workerCount <= 0 {
		workerCount = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(i)))

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				if !cfg.Infinite && atomic.LoadInt64(&s.sentTx) >= int64(cfg.TotalTx) {
					return
				}

				b := bots[r.Intn(len(bots))]

				priceMu.Lock()
				refPrice := currentPrice
				priceMu.Unlock()

				req := s.buildOrderRequest(b, cfg.Symbol, refPrice, r)

				if err := s.placeOrder(ctx, req); err != nil {
					// 餘額不足時自動充值後重試
					if isInsufficientFunds(err) {
						_ = s.rechargeBot(b.userID)
					} else {
						logger.Warn("下單失敗", zap.String("user_id", b.userID.String()), zap.Error(err))
					}
				} else {
					atomic.AddInt64(&s.sentTx, 1)
				}

				if cfg.IntervalMs > 0 {
					time.Sleep(time.Duration(cfg.IntervalMs) * time.Millisecond)
				}
			}
		}()
	}

	wg.Wait()
}

// buildOrderRequest 根據機器人角色建立訂單參數
func (s *Service) buildOrderRequest(b bot, symbol string, refPrice float64, r *rand.Rand) placeOrderRequest {
	side := "BUY"
	if r.Intn(2) == 0 {
		side = "SELL"
	}

	orderType := "LIMIT"
	price := refPrice

	if b.role == "TAKER" {
		if r.Float64() < 0.8 {
			orderType = "MARKET"
			price = 0
		} else {
			if side == "BUY" {
				price = refPrice * 1.02
			} else {
				price = refPrice * 0.98
			}
		}
	} else {
		// MAKER：在參考價格附近掛單
		spread := r.Float64() * 0.02 * refPrice
		if side == "BUY" {
			price = refPrice - spread
		} else {
			price = refPrice + spread
		}
	}

	qty := decimal.NewFromFloat(0.1 + r.Float64()).Round(4)
	p := decimal.NewFromFloat(price).Round(2)

	return placeOrderRequest{
		UserID:   b.userID,
		Symbol:   symbol,
		Side:     side,
		Type:     orderType,
		Price:    p,
		Quantity: qty,
	}
}

// placeOrder 呼叫 Gateway POST /api/v1/orders
func (s *Service) placeOrder(ctx context.Context, req placeOrderRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化請求失敗: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.gatewayURL+"/api/v1/orders", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("建立 HTTP 請求失敗: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// idempotency key（每次下單都是獨立新單）
	httpReq.Header.Set("Idempotency-Key", uuid.New().String())

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("HTTP 請求失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return fmt.Errorf("下單請求無效: %s", errBody.Error)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下單失敗，HTTP %d", resp.StatusCode)
	}
	return nil
}

// rechargeBot 呼叫 Gateway POST /api/v1/test/recharge/:user_id
func (s *Service) rechargeBot(userID uuid.UUID) error {
	url := fmt.Sprintf("%s/api/v1/test/recharge/%s", s.gatewayURL, userID.String())
	resp, err := s.httpClient.Post(url, "application/json", nil)
	if err != nil {
		return fmt.Errorf("充值請求失敗: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("充值失敗，HTTP %d", resp.StatusCode)
	}
	return nil
}

// isInsufficientFunds 判斷錯誤是否為餘額不足
func isInsufficientFunds(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errInsufficientFunds) ||
		containsString(err.Error(), "餘額不足", "insufficient")
}

var errInsufficientFunds = errors.New("餘額不足")

func containsString(s string, substrings ...string) bool {
	for _, sub := range substrings {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

func applyDefaults(cfg Config) Config {
	if cfg.Symbol == "" {
		cfg.Symbol = "BTC-USD"
	}
	if cfg.BasePrice <= 0 {
		cfg.BasePrice = 30000
	}
	if cfg.NumBots <= 0 {
		cfg.NumBots = 20
	}
	if cfg.TotalTx <= 0 && !cfg.Infinite {
		cfg.TotalTx = 500
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 5
	}
	if cfg.IntervalMs < 0 {
		cfg.IntervalMs = 100
	}
	return cfg
}
