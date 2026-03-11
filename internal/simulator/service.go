package simulator

import (
	"context"
	"errors"
	"log"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RayLiu1999/exchange/internal/core"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Config struct {
	Symbol      string  `json:"symbol"`
	BasePrice   float64 `json:"base_price"`
	NumTraders  int     `json:"num_traders"`
	TotalTx     int     `json:"total_tx"`
	WorkerCount int     `json:"worker_count"`
	Infinite    bool    `json:"infinite"`    // 無限模式，持續運行直到手動停止
	IntervalMs  int     `json:"interval_ms"` // 每筆訂單間隔 (毫秒)，0 表示不等待
}

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

type Trader struct {
	ID   uuid.UUID
	Role string
}

type Service struct {
	svc     core.ExchangeService
	mu      sync.Mutex
	status  Status
	running bool
	cancel  context.CancelFunc
	sentTx  int64
}

func NewService(svc core.ExchangeService) *Service {
	return &Service{svc: svc}
}

func (s *Service) Start(cfg Config) error {
	cfg = applyDefaultConfig(cfg)

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("模擬器已在運行中")
	}

	s.running = true
	s.sentTx = 0
	s.status = Status{
		Running:   true,
		Symbol:    cfg.Symbol,
		TotalTx:   cfg.TotalTx,
		Infinite:  cfg.Infinite,
		StartedAt: time.Now(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()

	go s.run(ctx, cfg)
	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return errors.New("模擬器未啟動")
	}
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *Service) GetStatus() Status {
	s.mu.Lock()
	status := s.status
	running := s.running
	s.mu.Unlock()

	status.Running = running
	status.SentTx = atomic.LoadInt64(&s.sentTx)
	return status
}

func (s *Service) run(ctx context.Context, cfg Config) {
	log.Printf("🚀 啟動模擬交易 | 幣種=%s | 交易量=%d | 交易者=%d", cfg.Symbol, cfg.TotalTx, cfg.NumTraders)
	traders := s.seedTraders(ctx, cfg.NumTraders)
	if len(traders) == 0 {
		s.finish("建立交易者失敗")
		return
	}

	s.runSimulation(ctx, traders, cfg)
	s.finish("模擬完成")
}

func (s *Service) finish(message string) {
	finishedAt := time.Now()

	s.mu.Lock()
	s.running = false
	s.status.Running = false
	s.status.FinishedAt = &finishedAt
	s.status.Message = message
	s.cancel = nil
	s.mu.Unlock()
}

func (s *Service) seedTraders(ctx context.Context, numTraders int) []Trader {
	traders := make([]Trader, 0, numTraders)
	for i := 0; i < numTraders; i++ {
		user, _, err := s.svc.RegisterAnonymousUser(ctx)
		if err != nil {
			log.Printf("❌ 建立交易者失敗: %v", err)
			continue
		}

		role := "TAKER"
		if i < numTraders/2 {
			role = "MAKER"
		}
		traders = append(traders, Trader{ID: user.ID, Role: role})
	}
	return traders
}

func (s *Service) runSimulation(ctx context.Context, traders []Trader, cfg Config) {
	currentPrice := cfg.BasePrice
	priceMu := sync.Mutex{}

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

	var wg sync.WaitGroup
	workerCount := cfg.WorkerCount
	if workerCount <= 0 {
		workerCount = 1
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
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
					// 為了保證其他 workers 也會提早退出，這裡也可以取消 ctx，但這會讓外面的 defer cancel 也提早觸發
					// 這裡依賴大家一起判斷 sentTx
					return
				}

				trader := traders[r.Intn(len(traders))]

				priceMu.Lock()
				refPrice := currentPrice
				priceMu.Unlock()

				side := core.SideBuy
				if r.Intn(2) == 0 {
					side = core.SideSell
				}

				orderType := core.TypeLimit
				price := refPrice

				if trader.Role == "TAKER" {
					if r.Float64() < 0.8 {
						orderType = core.TypeMarket
						price = 0
					} else {
						if side == core.SideBuy {
							price = refPrice * 1.02
						} else {
							price = refPrice * 0.98
						}
					}
				} else {
					spread := r.Float64() * 0.02 * refPrice
					if side == core.SideBuy {
						price = refPrice - spread
					} else {
						price = refPrice + spread
					}
				}

				qty := decimal.NewFromFloat(0.1 + r.Float64())

				order := &core.Order{
					UserID:   trader.ID,
					Symbol:   cfg.Symbol,
					Side:     side,
					Type:     orderType,
					Price:    decimal.NewFromFloat(price).Round(2), // 確保模擬的訂單符合前端的 2 位小數顯示 (Tick Size = 0.01)
					Quantity: qty.Round(4),                         // 數量也符合前端的顯示習慣
				}

				if err := s.svc.PlaceOrder(ctx, order); err != nil {
					log.Printf("❌ 模擬下單失敗: %v", err)
					if errors.Is(err, core.ErrInsufficientFunds) || strings.Contains(err.Error(), "餘額不足") {
						log.Printf("⚠️ 觸發自動加值...")
						s.svc.RechargeTestUser(ctx, trader.ID)
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

func applyDefaultConfig(cfg Config) Config {
	if cfg.Symbol == "" {
		cfg.Symbol = "BTC-USD"
	}
	if cfg.BasePrice <= 0 {
		cfg.BasePrice = 30000
	}
	if cfg.NumTraders <= 0 {
		cfg.NumTraders = 20
	}
	if cfg.TotalTx <= 0 && !cfg.Infinite {
		cfg.TotalTx = 100
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 1
	}
	if cfg.IntervalMs < 0 {
		cfg.IntervalMs = 300 // 預設放慢一點
	}
	return cfg
}
