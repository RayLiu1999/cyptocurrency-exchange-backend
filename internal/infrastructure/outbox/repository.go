package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository 提供 Outbox 訊息的 DB 存取操作
type Repository struct {
	pool *pgxpool.Pool
}

type dbExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// NewRepository 建立一個新的 Outbox Repository
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) getExecutor(ctx context.Context) dbExecutor {
	if tx := db.GetTx(ctx); tx != nil {
		return tx
	}
	return r.pool
}

// WithTx 在單一 DB Transaction 中執行 callback，供 Worker 持有 SKIP LOCKED 鎖直到批次處理完成。
func (r *Repository) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		txCtx := context.WithValue(ctx, db.TxKey, tx)
		return fn(txCtx)
	})
}

// Insert 在與業務事務（ExecTx）相同的 TX 內插入一筆 Outbox 記錄
// conn 為 pgx 的 Tx 或 Pool，透過 interface 接受兩者
func (r *Repository) Insert(ctx context.Context, msg *Message) error {
	msg.ID, _ = uuid.NewV7()
	msg.CreatedAt = time.Now().UnixMilli()
	msg.Status = StatusPending

	_, err := r.getExecutor(ctx).Exec(ctx, `
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

// BatchInsert 批次插入 Outbox 記錄（使用 CopyFrom，必須在 tx 中）
func (r *Repository) BatchInsert(ctx context.Context, msgs []*Message) error {
	if len(msgs) == 0 {
		return nil
	}
	tx := db.GetTx(ctx)
	if tx == nil {
		// 回退機制（雖然強烈建議在 transaction 內執行）
		return fmt.Errorf("BatchInsert must be called within ExecTx")
	}

	now := time.Now().UnixMilli()
	rows := make([][]any, 0, len(msgs))
	for _, msg := range msgs {
		msg.ID, _ = uuid.NewV7()
		msg.CreatedAt = now
		msg.Status = StatusPending
		rows = append(rows, []any{
			msg.ID, msg.AggregateID, msg.AggregateType, msg.Topic, msg.PartitionKey,
			msg.Payload, msg.Status, msg.RetryCount, msg.CreatedAt, msg.PublishedAt,
		})
	}

	_, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"outbox_messages"},
		[]string{"id", "aggregate_id", "aggregate_type", "topic", "partition_key", "payload", "status", "retry_count", "created_at", "published_at"},
		pgx.CopyFromRows(rows),
	)
	return err
}

// FetchPending 批次取出尚未發送的 Outbox 訊息（最多 batchSize 筆）
// 增加 gracePeriod 參數，確保只抓取建立時間超過冷靜期的訊息（避免與熱路徑發生 Race Condition）
// 使用 SKIP LOCKED 確保多個 Worker 實例不會取到同一批訊息
func (r *Repository) FetchPending(ctx context.Context, batchSize int, gracePeriod time.Duration) ([]*Message, error) {
	threshold := time.Now().Add(-gracePeriod).UnixMilli()
	rows, err := r.getExecutor(ctx).Query(ctx, `
		SELECT id, aggregate_id, aggregate_type, topic, partition_key, payload, status, retry_count, created_at, published_at
		FROM outbox_messages
		WHERE status = $1 AND created_at <= $2
		ORDER BY created_at ASC
		LIMIT $3
		FOR UPDATE SKIP LOCKED`,
		StatusPending, threshold, batchSize,
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

// MarkPublished 將已成功 Produce 到 Kafka 的訊息進行物理刪除（單筆）
func (r *Repository) MarkPublished(ctx context.Context, id uuid.UUID) error {
	_, err := r.getExecutor(ctx).Exec(ctx, `
		DELETE FROM outbox_messages
		WHERE id = $1`,
		id,
	)
	return err
}

// MarkPublishedBatch 將已成功 Produce 到 Kafka 的多筆訊息進行批次物理刪除
func (r *Repository) MarkPublishedBatch(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.getExecutor(ctx).Exec(ctx, `
		DELETE FROM outbox_messages
		WHERE id = ANY($1)`,
		ids,
	)
	return err
}

// IncrementRetry 當 Kafka Produce 失敗時，增加 retry_count
// 未來可以根據 retry_count 設計死信佇列（Dead Letter Queue）
func (r *Repository) IncrementRetry(ctx context.Context, id uuid.UUID) error {
	_, err := r.getExecutor(ctx).Exec(ctx, `
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
	err := r.getExecutor(ctx).QueryRow(ctx, `
		SELECT COUNT(*) FROM outbox_messages WHERE status = $1`,
		StatusPending,
	).Scan(&count)
	return count, err
}
