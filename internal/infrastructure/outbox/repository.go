package outbox

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository 提供 Outbox 訊息的 DB 存取操作
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 建立一個新的 Outbox Repository
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Insert 在與業務事務（ExecTx）相同的 TX 內插入一筆 Outbox 記錄
// conn 為 pgx 的 Tx 或 Pool，透過 interface 接受兩者
func (r *Repository) Insert(ctx context.Context, msg *Message) error {
	msg.ID, _ = uuid.NewV7()
	msg.CreatedAt = time.Now().UnixMilli()
	msg.Status = StatusPending

	_, err := r.pool.Exec(ctx, `
		INSERT INTO outbox_messages
			(id, aggregate_id, aggregate_type, topic, partition_key, payload, status, retry_count, created_at, published_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		msg.ID,
		msg.AggregateID,
		msg.AggregateType,
		msg.Topic,
		msg.PartitionKey,
		msg.Payload,
		msg.Status,
		msg.RetryCount,
		msg.CreatedAt,
		msg.PublishedAt,
	)
	return err
}

// FetchPending 批次取出尚未發送的 Outbox 訊息（最多 batchSize 筆）
// 使用 SKIP LOCKED 確保多個 Worker 實例不會取到同一批訊息
func (r *Repository) FetchPending(ctx context.Context, batchSize int) ([]*Message, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, aggregate_id, aggregate_type, topic, partition_key, payload, status, retry_count, created_at, published_at
		FROM outbox_messages
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED`,
		StatusPending, batchSize,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*Message
	for rows.Next() {
		m := &Message{}
		err := rows.Scan(
			&m.ID, &m.AggregateID, &m.AggregateType,
			&m.Topic, &m.PartitionKey, &m.Payload,
			&m.Status, &m.RetryCount, &m.CreatedAt, &m.PublishedAt,
		)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// MarkPublished 將已成功 Produce 到 Kafka 的訊息標記為 Published
func (r *Repository) MarkPublished(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE outbox_messages
		SET status = $1, published_at = $2
		WHERE id = $3`,
		StatusPublished, time.Now().UnixMilli(), id,
	)
	return err
}

// IncrementRetry 當 Kafka Produce 失敗時，增加 retry_count
// 未來可以根據 retry_count 設計死信佇列（Dead Letter Queue）
func (r *Repository) IncrementRetry(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE outbox_messages
		SET retry_count = retry_count + 1
		WHERE id = $1`,
		id,
	)
	return err
}

// CountPending 回傳目前 Pending 狀態的訊息總數（用於 Prometheus Gauge 指標）
func (r *Repository) CountPending(ctx context.Context) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM outbox_messages WHERE status = $1`,
		StatusPending,
	).Scan(&count)
	return count, err
}
