package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/election"
	"github.com/RayLiu1999/exchange/internal/infrastructure/kafka"
	"github.com/RayLiu1999/exchange/internal/infrastructure/logger"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	"github.com/RayLiu1999/exchange/internal/matching"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
	"github.com/RayLiu1999/exchange/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// matching-engine: 獨立的記憶體撮合引擎服務
// 消費 Kafka：exchange.orders（group: matching-engine，僅 Leader 實例處理）
// 發布 Kafka：exchange.settlements, exchange.trades, exchange.orderbook
// 無對外 API（僅 health check），無 WebSocket，執行期間無 DB 寫入
// Leader Election：透過 PostgreSQL partition_leader_locks 確保全局唯一 Leader，防止腦裂
func main() {
	defer logger.Sync()

	// 1. 資料庫連線（唯讀還原快照 + Leader Election 心跳）
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		logger.Log.Fatal("matching-engine: DATABASE_URL 環境變數未設定")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		logger.Log.Fatal("matching-engine: 無法連接資料庫", zap.Error(err))
	}
	defer pool.Close()

	// 2. Repository（供 RestoreEngineSnapshot 與 Leader Election 使用）
	repo := repository.NewPostgresRepository(pool)

	// 3. Redis（掛單簿快取更新）
	redisCfg := redis.DefaultConfig()
	if redisAddr := os.Getenv("REDIS_URL"); redisAddr != "" {
		redisCfg.Addr = redisAddr
	}
	redisClient, err := redis.NewClient(redisCfg)
	if err != nil {
		logger.Log.Warn("matching-engine: Redis 不可用，掛單簿快取已停用", zap.Error(err))
	}
	var cacheRepo domain.CacheRepository
	if redisClient != nil {
		cacheRepo = repository.NewRedisCacheRepository(redisClient)
		logger.Log.Info("Redis 已連線（掛單簿快取）")
	}

	// 4. Kafka Producer（發布 settlements / trades / orderbook 事件）
	kafkaCfg := kafka.DefaultConfig()
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		kafkaCfg.Brokers = strings.Split(brokers, ",")
	}
	if resetOffset := os.Getenv("KAFKA_RESET_OFFSET"); resetOffset != "" {
		kafkaCfg.ResetOffset = strings.ToLower(resetOffset)
	}
	if os.Getenv("KAFKA_ALLOW_AUTO_CREATE") == "false" {
		kafkaCfg.AllowAutoTopicCreation = false
	} else if os.Getenv("KAFKA_ALLOW_AUTO_CREATE") == "true" {
		kafkaCfg.AllowAutoTopicCreation = true
	}

	producer, err := kafka.NewProducer(kafkaCfg)
	if err != nil {
		logger.Log.Fatal("matching-engine: Kafka Producer 連線失敗", zap.Error(err))
	}
	defer producer.Close()
	logger.Log.Info("Kafka Producer 已連線")

	// 5. 撮合引擎服務（記憶體引擎，等待 Leader Election 後才啟動 Consumer）
	engineManager := engine.NewEngineManager()
	svc := matching.NewSubscriber(engineManager, producer, cacheRepo)

	// 6. Leader Election 設定
	// instanceID 使用 Hostname（在 Docker/K8s 中為容器的唯一識別名稱）
	instanceID, err := os.Hostname()
	if err != nil {
		logger.Log.Fatal("matching-engine: 無法取得 Hostname 作為實例 ID", zap.Error(err))
	}
	electionRepo := election.NewRepository(pool)
	elector := election.NewElector(electionRepo, "matching-engine:global", instanceID)

	// 7. Consumer 生命週期管理（需要 Mutex 保護，因為 Elector 跑在獨立 Goroutine）
	var (
		consumerMu     sync.Mutex
		matchConsumer  *kafka.Consumer
		consumerCancel context.CancelFunc
	)

	// onBecomeLeader：成為 Leader 時的回呼
	// 執行冷啟動（DB 還原快照）並啟動 Kafka Consumer 開始消費訂單事件
	onBecomeLeader := func() {
		consumerMu.Lock()
		defer consumerMu.Unlock()

		logger.Log.Info("✅ 已成為 Leader，開始執行冷啟動流程",
			zap.String("instanceID", instanceID),
		)

		// 清除記憶體中的舊狀態，防止上一任 Leader 殘留的掛單資料污染
		engineManager.Reset()

		// 設定新的 FencingToken，從此撮合引擎發出的所有結算訊息都帶有此号碼
		svc.SetFencingToken(elector.FencingToken())
		logger.Log.Info("已設定 FencingToken",
			zap.Int64("fencing_token", elector.FencingToken()),
		)

		// 從 DB 還原活動訂單至記憶體引擎（建立正確的掛單簿基準狀態）
		logger.Log.Info("正在從 DB 還原撮合引擎快照...")
		if err := matching.RestoreEngineSnapshot(context.Background(), repo, engineManager); err != nil {
			logger.Log.Error("RestoreEngineSnapshot 失敗", zap.Error(err))
		}
		logger.Log.Info("撮合引擎快照還原完成")

		// 預先建立 Kafka Topics（避免 UNKNOWN_TOPIC_OR_PARTITION 錯誤）
		topicCtx, topicCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer topicCancel()
		if err := producer.CreateTopics(topicCtx, []string{
			domain.TopicOrders,
			domain.TopicSettlements,
			domain.TopicTrades,
			domain.TopicOrderBook,
			domain.TopicOrderUpdates,
		}); err != nil {
			logger.Log.Warn("CreateTopics 失敗（主題可能已存在）", zap.Error(err))
		} else {
			logger.Log.Info("Kafka topics 已初始化")
		}

		// 同步掛單簿快照至 Redis 與市場資料流（冷啟動後讓前端立即看到正確行情）
		restoredSymbols := svc.SyncRecoveredOrderBooks(20)
		if len(restoredSymbols) == 0 {
			logger.Log.Info("冷啟動後沒有需要同步的掛單簿快照")
		} else {
			logger.Log.Info("冷啟動掛單簿快照已同步至 Redis 與市場資料流",
				zap.Strings("symbols", restoredSymbols),
			)
		}

		// 建立 Consumer 的獨立 Context，讓 onLoseLeadership 可以單獨取消
		consumerCtx, cancel := context.WithCancel(context.Background())
		consumerCancel = cancel

		consumer, consumerErr := kafka.NewConsumer(kafkaCfg, "matching-engine", []string{domain.TopicOrders})
		if consumerErr != nil {
			logger.Log.Error("matching-engine: 建立 matching consumer 失敗", zap.Error(consumerErr))
			cancel()
			return
		}
		matchConsumer = consumer
		matchConsumer.Start(consumerCtx, svc.HandleEvents)
		logger.Log.Info("Kafka matching consumer 已啟動", zap.String("topic", domain.TopicOrders))
	}

	// onLoseLeadership：失去 Leader 時的回呼
	// 立刻停止 Kafka Consumer，防止 Stale Leader 繼續撮合產生雙重結算
	onLoseLeadership := func() {
		consumerMu.Lock()
		defer consumerMu.Unlock()

		logger.Log.Warn("⚠️ 已失去 Leader 身份，正在停止 Kafka Consumer",
			zap.String("instanceID", instanceID),
		)
		// 先將 FencingToken 清零，讓任何還在執行的 HandleEvents 拒絕實際撮合
		svc.SetFencingToken(0)
		if consumerCancel != nil {
			consumerCancel()
			consumerCancel = nil
		}
		if matchConsumer != nil {
			matchConsumer.Wait()
			matchConsumer = nil
		}
		logger.Log.Info("Kafka Consumer 已停止，進入 Standby 模式")
	}

	// 8. 啟動全局 Context 與 Leader Elector（跑在背景 Goroutine，非阻塞）
	globalCtx, cancelGlobal := context.WithCancel(context.Background())
	defer cancelGlobal()

	go elector.Run(globalCtx, onBecomeLeader, onLoseLeadership)
	logger.Log.Info("Leader Elector 已啟動，等待競選結果...",
		zap.String("instanceID", instanceID),
		zap.String("partition", "matching-engine:global"),
	)

	// 9. Health check + metrics 端點
	// 無論是否為 Leader，此端點都必須回應，供 Load Balancer 判斷容器存活
	// is_leader 欄位讓運維人員快速判斷目前哪台實例為主
	r := gin.New()
	r.Use(metrics.Middleware("matching-engine"))
	r.GET("/metrics", gin.WrapH(metrics.Handler()))
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":     "ok",
			"service":    "matching-engine",
			"is_leader":  elector.IsLeader(),
			"instanceID": instanceID,
		})
	})
	port := os.Getenv("MATCHING_ENGINE_PORT")
	if port == "" {
		port = "8101"
	}

	srv := &http.Server{Addr: ":" + port, Handler: r}
	go func() {
		logger.Log.Info("Health check server 已啟動", zap.String("port", ":"+port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Error("Health check server 發生錯誤", zap.Error(err))
		}
	}()

	// 10. 優雅關機
	// 取消全局 Context 後，Elector 的 Run() 會自動釋放 Leader 鎖，加速 Standby 接管
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Log.Info("收到關閉訊號", zap.String("signal", sig.String()))

	// 取消 Context：同時觸發 Elector 釋放鎖 + Consumer 停止消費
	cancelGlobal()

	// 等待正在處理的 Kafka 訊息完整處理完畢（優雅停機，不丟失正在撮合的訂單）
	consumerMu.Lock()
	if matchConsumer != nil {
		matchConsumer.Wait()
	}
	consumerMu.Unlock()

	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(ctxShutdown); err != nil {
		logger.Log.Error("Health check server 關閉失敗", zap.Error(err))
	}
	logger.Log.Info("matching-engine 優雅關機完成")
}
