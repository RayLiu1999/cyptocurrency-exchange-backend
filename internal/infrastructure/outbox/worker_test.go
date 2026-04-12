package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockWorkerRepo struct {
	mu              sync.Mutex
	pendingCount    int64
	countErr        error
	fetchMessages   []*Message
	fetchErr        error
	retryErr        error
	markErr         error
	withTxCalls     int
	fetchCalls      int
	lastBatchSize   int
	lastGracePeriod time.Duration
	retryIDs        []uuid.UUID
	markedIDs       []uuid.UUID
}

func (r *mockWorkerRepo) CountPending(ctx context.Context) (int64, error) {
	return r.pendingCount, r.countErr
}

func (r *mockWorkerRepo) WithTx(ctx context.Context, fn func(context.Context) error) error {
	r.mu.Lock()
	r.withTxCalls++
	r.mu.Unlock()
	return fn(ctx)
}

func (r *mockWorkerRepo) FetchPending(ctx context.Context, batchSize int, gracePeriod time.Duration) ([]*Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.fetchCalls++
	r.lastBatchSize = batchSize
	r.lastGracePeriod = gracePeriod
	if r.fetchErr != nil {
		return nil, r.fetchErr
	}

	msgs := make([]*Message, 0, len(r.fetchMessages))
	for _, msg := range r.fetchMessages {
		clone := *msg
		msgs = append(msgs, &clone)
	}
	return msgs, nil
}

func (r *mockWorkerRepo) IncrementRetry(ctx context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retryIDs = append(r.retryIDs, id)
	return r.retryErr
}

func (r *mockWorkerRepo) MarkPublishedBatch(ctx context.Context, ids []uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markedIDs = append(r.markedIDs, ids...)
	return r.markErr
}

type mockWorkerPublisher struct {
	mu        sync.Mutex
	failKeys  map[string]error
	published []string
}

func (p *mockWorkerPublisher) PublishRaw(ctx context.Context, topic, partitionKey string, value []byte) error {
	if err := p.failKeys[partitionKey]; err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, topic+":"+partitionKey)
	return nil
}

func (p *mockWorkerPublisher) Published() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.published))
	copy(out, p.published)
	return out
}

func newWorkerTestMessage(t *testing.T, partitionKey string) *Message {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return &Message{
		ID:           id,
		Topic:        "exchange.orders",
		PartitionKey: partitionKey,
		Payload:      []byte(`{}`),
	}
}

func TestWorkerProcess_PublishesAndDeletesSuccessfulBatch(t *testing.T) {
	msg1 := newWorkerTestMessage(t, "BTC-USD")
	msg2 := newWorkerTestMessage(t, "ETH-USD")
	repo := &mockWorkerRepo{
		pendingCount:  2,
		fetchMessages: []*Message{msg1, msg2},
	}
	publisher := &mockWorkerPublisher{}
	worker := &Worker{repo: repo, publisher: publisher, batchSize: 100}

	worker.process(context.Background())

	assert.Equal(t, 1, repo.withTxCalls)
	assert.Equal(t, 1, repo.fetchCalls)
	assert.Equal(t, 100, repo.lastBatchSize)
	assert.Equal(t, 5*time.Second, repo.lastGracePeriod)
	assert.ElementsMatch(t, []uuid.UUID{msg1.ID, msg2.ID}, repo.markedIDs)
	assert.Empty(t, repo.retryIDs)
	assert.ElementsMatch(t, []string{"exchange.orders:BTC-USD", "exchange.orders:ETH-USD"}, publisher.Published())
}

func TestWorkerProcess_IncrementsRetryAndSkipsFailedDeleteBatch(t *testing.T) {
	msg1 := newWorkerTestMessage(t, "BTC-USD")
	msg2 := newWorkerTestMessage(t, "ETH-USD")
	repo := &mockWorkerRepo{
		pendingCount:  2,
		fetchMessages: []*Message{msg1, msg2},
	}
	publisher := &mockWorkerPublisher{
		failKeys: map[string]error{"ETH-USD": errors.New("kafka unavailable")},
	}
	worker := &Worker{repo: repo, publisher: publisher, batchSize: 100}

	worker.process(context.Background())

	assert.ElementsMatch(t, []uuid.UUID{msg2.ID}, repo.retryIDs)
	assert.ElementsMatch(t, []uuid.UUID{msg1.ID}, repo.markedIDs)
	assert.Equal(t, []string{"exchange.orders:BTC-USD"}, publisher.Published())
}

func TestWorkerProcess_StopsWhenFetchPendingFails(t *testing.T) {
	repo := &mockWorkerRepo{
		pendingCount: 1,
		fetchErr:     errors.New("db unavailable"),
	}
	publisher := &mockWorkerPublisher{}
	worker := &Worker{repo: repo, publisher: publisher, batchSize: 10}

	worker.process(context.Background())

	assert.Equal(t, 1, repo.withTxCalls)
	assert.Equal(t, 1, repo.fetchCalls)
	assert.Empty(t, repo.retryIDs)
	assert.Empty(t, repo.markedIDs)
	assert.Empty(t, publisher.Published())
}
