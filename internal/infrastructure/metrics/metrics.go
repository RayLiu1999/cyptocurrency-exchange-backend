package metrics

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

var (
	registerOnce = sync.Once{}
	requestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "exchange",
			Name:      "http_requests_total",
			Help:      "HTTP 請求總數。",
		},
		[]string{"service", "method", "path", "status"},
	)
	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "exchange",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP 請求延遲分佈。",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"service", "method", "path", "status"},
	)
	inFlightRequests = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "exchange",
			Name:      "http_in_flight_requests",
			Help:      "目前處理中的 HTTP 請求數。",
		},
		[]string{"service"},
	)
	ordersTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "exchange",
			Name:      "orders_total",
			Help:      "下單請求總數。",
		},
		[]string{"mode", "side", "type", "result"},
	)
	orderDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "exchange",
			Name:      "order_processing_duration_seconds",
			Help:      "下單處理延遲分佈。",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"mode", "side", "type", "result"},
	)
	tradesExecutedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "exchange",
			Name:      "trades_executed_total",
			Help:      "完成結算的成交筆數。",
		},
		[]string{"mode"},
	)
	kafkaEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "exchange",
			Name:      "kafka_events_total",
			Help:      "Kafka 事件處理總數。",
		},
		[]string{"component", "handler", "result"},
	)
	kafkaEventDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "exchange",
			Name:      "kafka_event_duration_seconds",
			Help:      "Kafka 事件處理延遲分佈。",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"component", "handler", "result"},
	)
	websocketConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "exchange",
			Name:      "websocket_connections",
			Help:      "目前存活中的 WebSocket 連線數。",
		},
		[]string{"service"},
	)
	websocketBroadcastTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "exchange",
			Name:      "websocket_broadcast_total",
			Help:      "WebSocket 廣播訊息總數。",
		},
		[]string{"service", "type", "result"},
	)
	websocketBroadcastDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "exchange",
			Name:      "websocket_broadcast_duration_seconds",
			Help:      "WebSocket 廣播完成耗時。",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"service", "type"},
	)
	websocketDroppedClientsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "exchange",
			Name:      "websocket_dropped_clients_total",
			Help:      "WebSocket 因緩衝區滿被丟棄的訊息或斷開的客戶端數量。",
		},
		[]string{"service", "reason"},
	)

	// Outbox Pattern 指標
	outboxPendingCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "exchange",
			Name:      "outbox_pending_count",
			Help:      "目前 outbox_messages 表中尚未發送到 Kafka 的訊息積壓數量（Pending 狀態）。若此值持續上升代表 Outbox Worker 或 Kafka 異常。",
		},
	)
	outboxPublishTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "exchange",
			Name:      "outbox_publish_total",
			Help:      "Outbox Worker 發送訊息到 Kafka 的總次數。",
		},
		[]string{"result"}, // result: success | error
	)
	outboxPublishLatency = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "exchange",
			Name:      "outbox_publish_latency_seconds",
			Help:      "Outbox Worker 發送單筆訊息到 Kafka 的延遲分佈。",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		},
	)

	// Leader Election 指標
	isPartitionLeader = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "exchange",
			Name:      "is_partition_leader",
			Help:      "目前此實例是否為 Leader：1 = Leader，0 = Standby。若所有實例的總和 > 1 代表腦裂；總和 = 0 代表無 Leader。",
		},
		[]string{"partition"},
	)
	leaderLeaseRenewalsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "exchange",
			Name:      "leader_lease_renewals_total",
			Help:      "Leader 租約延長操作的總次數。",
		},
		[]string{"result"}, // result: success | error
	)
)

// Ensure 初始化 Prometheus 指標，並避免重複註冊。
func Ensure() {
	registerOnce.Do(func() {
		prometheus.MustRegister(requestTotal)
		prometheus.MustRegister(requestDuration)
		prometheus.MustRegister(inFlightRequests)
		prometheus.MustRegister(ordersTotal)
		prometheus.MustRegister(orderDuration)
		prometheus.MustRegister(tradesExecutedTotal)
		prometheus.MustRegister(kafkaEventsTotal)
		prometheus.MustRegister(kafkaEventDuration)
		prometheus.MustRegister(websocketConnections)
		prometheus.MustRegister(websocketBroadcastTotal)
		prometheus.MustRegister(websocketBroadcastDuration)
		prometheus.MustRegister(websocketDroppedClientsTotal)
		prometheus.MustRegister(outboxPendingCount)
		prometheus.MustRegister(outboxPublishTotal)
		prometheus.MustRegister(outboxPublishLatency)
		prometheus.MustRegister(isPartitionLeader)
		prometheus.MustRegister(leaderLeaseRenewalsTotal)
	})
}

// Handler 回傳 Prometheus 的 metrics handler。
func Handler() http.Handler {
	Ensure()
	return promhttp.Handler()
}

// Middleware 量測 HTTP 請求數量、延遲與 in-flight 請求數。
func Middleware(serviceName string) gin.HandlerFunc {
	Ensure()

	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}

		start := time.Now()
		inFlightRequests.WithLabelValues(serviceName).Inc()
		defer inFlightRequests.WithLabelValues(serviceName).Dec()

		c.Next()

		statusLabel := fmt.Sprintf("%d", c.Writer.Status())
		pathLabel := c.FullPath()
		if pathLabel == "" {
			pathLabel = c.Request.URL.Path
		}

		requestTotal.WithLabelValues(serviceName, c.Request.Method, pathLabel, statusLabel).Inc()
		requestDuration.WithLabelValues(serviceName, c.Request.Method, pathLabel, statusLabel).Observe(time.Since(start).Seconds())
	}
}

func resultLabel(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, http.ErrAbortHandler) {
		return "aborted"
	}
	return "error"
}

// ObserveOrder 記錄下單流程結果與延遲。
func ObserveOrder(mode, side, orderType string, err error, duration time.Duration) {
	Ensure()
	result := resultLabel(err)
	ordersTotal.WithLabelValues(mode, side, orderType, result).Inc()
	orderDuration.WithLabelValues(mode, side, orderType, result).Observe(duration.Seconds())
}

// AddTradesExecuted 累加完成結算的成交筆數。
func AddTradesExecuted(mode string, count int) {
	Ensure()
	if count <= 0 {
		return
	}
	tradesExecutedTotal.WithLabelValues(mode).Add(float64(count))
}

// ObserveKafkaEvent 記錄 Kafka consumer handler 的結果與延遲。
func ObserveKafkaEvent(component, handler string, err error, duration time.Duration) {
	Ensure()
	result := resultLabel(err)
	kafkaEventsTotal.WithLabelValues(component, handler, result).Inc()
	kafkaEventDuration.WithLabelValues(component, handler, result).Observe(duration.Seconds())
}

// WebSocketConnected 增加目前 WebSocket 存活連線數。
func WebSocketConnected(service string) {
	Ensure()
	websocketConnections.WithLabelValues(service).Inc()
}

// WebSocketDisconnected 減少目前 WebSocket 存活連線數。
func WebSocketDisconnected(service string) {
	Ensure()
	websocketConnections.WithLabelValues(service).Dec()
}

// RecordWebSocketBroadcast 記錄 WebSocket 廣播訊息結果。
func RecordWebSocketBroadcast(service, messageType, result string) {
	Ensure()
	websocketBroadcastTotal.WithLabelValues(service, messageType, result).Inc()
}

// ObserveWebSocketBroadcastDuration 記錄 WebSocket 廣播完成耗時。
func ObserveWebSocketBroadcastDuration(service, messageType string, duration time.Duration) {
	Ensure()
	websocketBroadcastDuration.WithLabelValues(service, messageType).Observe(duration.Seconds())
}

// RecordWebSocketDroppedClient 記錄 WebSocket 丟棄客戶端或訊息次數。
func RecordWebSocketDroppedClient(service, reason string) {
	Ensure()
	websocketDroppedClientsTotal.WithLabelValues(service, reason).Inc()
}

// --- Outbox Pattern 指標 ---

// SetOutboxPendingCount 設定目前積壓中（Pending）的 Outbox 訊息數量。
// 應在 Outbox Worker 每次掃描前呼叫，確保 Grafana 能及時觸發 Alert。
func SetOutboxPendingCount(count float64) {
	Ensure()
	outboxPendingCount.Set(count)
}

// ObserveOutboxPublish 記錄 Outbox Worker 發送一筆訊息到 Kafka 的結果與延遲。
// result 應為 "success" 或 "error"。
func ObserveOutboxPublish(result string, duration time.Duration) {
	Ensure()
	outboxPublishTotal.WithLabelValues(result).Inc()
	outboxPublishLatency.Observe(duration.Seconds())
}

// RegisterDBStats 為特定名稱的 DB Pool 註冊定期/動態擷取的狀態指標。
func RegisterDBStats(dbName string, pool *pgxpool.Pool) {
	Ensure()

	labels := prometheus.Labels{"db_name": dbName}

	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace:   "exchange",
			Subsystem:   "db",
			Name:        "total_connections",
			Help:        "目前連線池的總連線數 (pgxpool total)",
			ConstLabels: labels,
		},
		func() float64 { return float64(pool.Stat().TotalConns()) },
	))

	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace:   "exchange",
			Subsystem:   "db",
			Name:        "idle_connections",
			Help:        "目前連線池的閒置連線數 (pgxpool idle)",
			ConstLabels: labels,
		},
		func() float64 { return float64(pool.Stat().IdleConns()) },
	))

	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace:   "exchange",
			Subsystem:   "db",
			Name:        "acquired_connections",
			Help:        "目前連線池被佔用的連線數 (pgxpool acquired)",
			ConstLabels: labels,
		},
		func() float64 { return float64(pool.Stat().AcquiredConns()) },
	))

	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace:   "exchange",
			Subsystem:   "db",
			Name:        "wait_count_total",
			Help:        "因連線池滿而等待的總次數 (pgxpool empty_acquire_count)",
			ConstLabels: labels,
		},
		func() float64 { return float64(pool.Stat().EmptyAcquireCount()) },
	))

	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace:   "exchange",
			Subsystem:   "db",
			Name:        "wait_duration_seconds_total",
			Help:        "因連線池滿而等待的總時間(秒) (pgxpool acquire_duration)",
			ConstLabels: labels,
		},
		func() float64 { return pool.Stat().AcquireDuration().Seconds() },
	))
}

// RegisterRedisStats 為特定名稱的 Redis Client 註冊定期/動態擷取的狀態指標。
func RegisterRedisStats(poolName string, client *redis.Client) {
	Ensure()

	labels := prometheus.Labels{"pool_name": poolName}

	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace:   "exchange",
			Subsystem:   "redis",
			Name:        "total_connections",
			Help:        "目前連線池的總連線數 (go-redis total)",
			ConstLabels: labels,
		},
		func() float64 { return float64(client.PoolStats().TotalConns) },
	))

	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace:   "exchange",
			Subsystem:   "redis",
			Name:        "idle_connections",
			Help:        "目前連線池的閒置連線數 (go-redis idle)",
			ConstLabels: labels,
		},
		func() float64 { return float64(client.PoolStats().IdleConns) },
	))

	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace:   "exchange",
			Subsystem:   "redis",
			Name:        "stale_connections",
			Help:        "目前連線池的過期連線數 (go-redis stale)",
			ConstLabels: labels,
		},
		func() float64 { return float64(client.PoolStats().StaleConns) },
	))

	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace:   "exchange",
			Subsystem:   "redis",
			Name:        "hits_total",
			Help:        "連線池取得連線命中的總次數 (go-redis hits)",
			ConstLabels: labels,
		},
		func() float64 { return float64(client.PoolStats().Hits) },
	))

	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace:   "exchange",
			Subsystem:   "redis",
			Name:        "timeouts_total",
			Help:        "連線池取得連線超時的總次數 (go-redis timeouts)",
			ConstLabels: labels,
		},
		func() float64 { return float64(client.PoolStats().Timeouts) },
	))
}

// --- Leader Election 指標 ---

// SetPartitionLeader 設定目前 Partition 的 Leader 狀態。
// isLeader=true 設為 1，否則設為 0。
// 建議在 LeaderElector 的 onBecomeLeader 與 onLoseLeadership 回呼中呼叫。
func SetPartitionLeader(partition string, isLeader bool) {
	Ensure()
	val := float64(0)
	if isLeader {
		val = 1
	}
	isPartitionLeader.WithLabelValues(partition).Set(val)
}

// ObserveLeaderRenewal 記錄一次租約延長的結果。
// result 應為 "success" 或 "error"。
// 若 error 次數突然飆增，代表 DB 連線不穩或即將發生選主切換。
func ObserveLeaderRenewal(result string) {
	Ensure()
	leaderLeaseRenewalsTotal.WithLabelValues(result).Inc()
}
