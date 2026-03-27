package outbox

import "github.com/google/uuid"

// Status 代表 Outbox 訊息的發送狀態
type Status int

const (
	// StatusPending 表示訊息尚未被發送到 Kafka
	StatusPending Status = 0
	// StatusPublished 表示訊息已成功發送到 Kafka
	StatusPublished Status = 1
)

// Message 代表一筆待發送到 Kafka 的 Outbox 記錄
// 設計原則：與業務事件 (PlaceOrder, CancelOrder) 的 DB 事務在同一個 TX 內寫入
// 確保「訂單寫入 DB」與「事件送到 Kafka」的一致性
type Message struct {
	ID             uuid.UUID // 全域唯一識別碼（UUID v7，時間序列友好）
	AggregateID    string    // 事件所屬的業務 ID（例如：OrderID 或 Symbol）
	AggregateType  string    // 事件類型標籤（例如：order, cancel_order）
	Topic          string    // 目標 Kafka topic
	PartitionKey   string    // Kafka partition key（通常是 symbol，確保同一交易對有序）
	Payload        []byte    // 序列化後的事件 payload（JSON）
	Status         Status    // 發送狀態：0=Pending, 1=Published
	RetryCount     int       // 已重試次數
	CreatedAt      int64     // 建立時間（Unix 毫秒）
	PublishedAt    int64     // 成功發送時間（Unix 毫秒，初始為 0）
}
