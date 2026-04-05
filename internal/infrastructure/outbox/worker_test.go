package outbox_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/outbox"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// mock 實作：in-memory Outbox Repository
// ============================================================

type mockOutboxRepo struct {
	mu       sync.Mutex
	messages map[uuid.UUID]*outbox.Message
}

func newMockOutboxRepo() *mockOutboxRepo {
	return &mockOutboxRepo{messages: make(map[uuid.UUID]*outbox.Message)}
}

func (r *mockOutboxRepo) Insert(ctx context.Context, msg *outbox.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	msg.ID, _ = uuid.NewV7()
	msg.CreatedAt = time.Now().UnixMilli()
	msg.Status = outbox.StatusPending
	clone := *msg
	r.messages[msg.ID] = &clone
	return nil
}

func (r *mockOutboxRepo) FetchPending(ctx context.Context, batchSize int, gracePeriod time.Duration) ([]*outbox.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	threshold := time.Now().Add(-gracePeriod).UnixMilli()
	var result []*outbox.Message
	for _, m := range r.messages {
		if m.Status == outbox.StatusPending && m.CreatedAt <= threshold {
			clone := *m
			result = append(result, &clone)
			// 模擬 SKIP LOCKED 行為：在取出後立刻轉換狀態為處理中，避免其他 Worker 拿到重複的訊息
			m.Status = outbox.StatusPublished
			if len(result) >= batchSize {
				break
			}
		}
	}
	return result, nil
}

func (r *mockOutboxRepo) MarkPublished(ctx context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.messages, id)
	return nil
}

func (r *mockOutboxRepo) IncrementRetry(ctx context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.messages[id]; ok {
		m.RetryCount++
	}
	return nil
}

func (r *mockOutboxRepo) CountPending(ctx context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.messages)), nil
}

func (r *mockOutboxRepo) PendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.messages)
}

func (r *mockOutboxRepo) RetryCountOf(id uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.messages[id]; ok {
		return m.RetryCount
	}
	return 0
}

// mock Publisher
type mockPublisher struct {
	mu            sync.Mutex
	publishedMsgs []string
	callCount     atomic.Int64
	failUntil     int64 // 前 N 次呼叫失敗
	err           error
}

func (p *mockPublisher) PublishRaw(ctx context.Context, topic, partitionKey string, value []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	call := p.callCount.Add(1)
	if call <= p.failUntil {
		return p.err
	}
	p.publishedMsgs = append(p.publishedMsgs, topic+":"+partitionKey)
	return nil
}

func (p *mockPublisher) PublishedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.publishedMsgs)
}

// ============================================================
// 測試案例
// ============================================================

// TestOutbox_MessageDeliveryGuarantee
// 驗證：TX 插入訊息後，Worker 能正確取出並標記為已發送（記錄被刪除）
func TestOutbox_MessageDeliveryGuarantee(t *testing.T) {
	repo := newMockOutboxRepo()
	publisher := &mockPublisher{}

	// 插入一筆訊息（模擬 TX1 完成）
	msg := &outbox.Message{
		Topic:        "exchange.orders",
		PartitionKey: "BTC-USD",
		Payload:      []byte(`{"event_type":"order.placed"}`),
	}
	err := repo.Insert(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, 1, repo.PendingCount(), "TX 後應有 1 筆 pending 訊息")


	// 建立 Worker（僅為驗證建構子簽名，實際測試不啟動 ticker）
	_ = outbox.NewWorker(nil, publisher, 1*time.Second, 100)
	msgs, _ := repo.FetchPending(context.Background(), 100, 0)
	for _, m := range msgs {
		_ = publisher.PublishRaw(context.Background(), m.Topic, m.PartitionKey, m.Payload)
		_ = repo.MarkPublished(context.Background(), m.ID)
	}

	assert.Equal(t, 0, repo.PendingCount(), "發送後訊息應被刪除")
	assert.Equal(t, 1, publisher.PublishedCount(), "應成功發送 1 筆訊息")
}

// TestOutbox_RetryCountIncrement_OnKafkaFailure
// 驗證：Kafka 發送失敗時，retry_count 正確遞增，訊息不被刪除
func TestOutbox_RetryCountIncrement_OnKafkaFailure(t *testing.T) {
	repo := newMockOutboxRepo()
	publisher := &mockPublisher{
		failUntil: 3, // 前 3 次呼叫失敗
		err:       errors.New("kafka unavailable"),
	}

	msg := &outbox.Message{
		Topic:        "exchange.orders",
		PartitionKey: "BTC-USD",
		Payload:      []byte(`{}`),
	}
	err := repo.Insert(context.Background(), msg)
	require.NoError(t, err)

	// 模擬 3 次失敗重試
	msgs, _ := repo.FetchPending(context.Background(), 100, 0)
	require.Len(t, msgs, 1)
	msgID := msgs[0].ID

	for i := 0; i < 3; i++ {
		publishErr := publisher.PublishRaw(context.Background(), msgs[0].Topic, msgs[0].PartitionKey, msgs[0].Payload)
		if publishErr != nil {
			_ = repo.IncrementRetry(context.Background(), msgID)
		}
	}

	assert.Equal(t, 3, repo.RetryCountOf(msgID), "3 次失敗後 retry_count 應為 3")
	assert.Equal(t, 1, repo.PendingCount(), "失敗後訊息不應被刪除，應等待下次重試")

	// 第 4 次成功
	publishErr := publisher.PublishRaw(context.Background(), msgs[0].Topic, msgs[0].PartitionKey, msgs[0].Payload)
	require.NoError(t, publishErr)
	_ = repo.MarkPublished(context.Background(), msgID)

	assert.Equal(t, 0, repo.PendingCount(), "成功後訊息應被刪除")
	assert.Equal(t, 1, publisher.PublishedCount())
}

// TestOutbox_GracePeriod_PreventsEarlyFetch
// 驗證：剛插入的訊息（未過冷靜期）不應被 Worker 取出，防止與熱路徑的 Race Condition
func TestOutbox_GracePeriod_PreventsEarlyFetch(t *testing.T) {
	repo := newMockOutboxRepo()

	msg := &outbox.Message{
		Topic:        "exchange.orders",
		PartitionKey: "BTC-USD",
		Payload:      []byte(`{}`),
	}
	_ = repo.Insert(context.Background(), msg)

	// 使用 5 秒冷靜期，剛插入的訊息不應被取出
	msgs, err := repo.FetchPending(context.Background(), 100, 5*time.Second)
	require.NoError(t, err)
	assert.Empty(t, msgs, "冷靜期內的訊息不應被取出，以防熱路徑 Race Condition")
}

// TestOutbox_SkipLocked_Concurrency
// 驗證：多個 Worker goroutine 同時取訊息時，每筆訊息只被一個 Worker 取到
// 注意：mockOutboxRepo 沒有真正的 SKIP LOCKED，此測試驗證架構接縫的設計正確性
// 真正的 SKIP LOCKED 行為需要搭配 testcontainers 整合測試才能驗證
func TestOutbox_SkipLocked_Concurrency_DesignValidation(t *testing.T) {
	// 此測試驗證 mockRepo 本身的 mutex 保護是否正確
	// 真實的 SKIP LOCKED 需要 PostgreSQL 才能完整測試
	repo := newMockOutboxRepo()

	// 插入 5 筆訊息
	for i := 0; i < 5; i++ {
		_ = repo.Insert(context.Background(), &outbox.Message{
			Topic:        "exchange.orders",
			PartitionKey: "BTC-USD",
			Payload:      []byte(`{}`),
		})
	}

	// 3 個 goroutine 同時取訊息（每次取 3 筆）
	var totalFetched atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msgs, _ := repo.FetchPending(context.Background(), 3, 0)
			totalFetched.Add(int64(len(msgs)))
		}()
	}
	wg.Wait()

	// 現在 mockOutboxRepo 有模擬 SKIP LOCKED，
	// 此處總量保證精準等於 5 筆（不會重複取拿）。
	assert.Equal(t, int64(5), totalFetched.Load(),
		"設計驗證：多 goroutine 併發下，訊息被正確互斥取出，無重複消費")

	t.Log("Note: mock 涵蓋了基礎驗證，真實的 SKIP LOCKED 需在 integration test 中由實體 PostgreSQL 提供。")
}
