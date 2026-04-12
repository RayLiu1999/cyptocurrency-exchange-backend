//go:build integration

package outbox

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/RayLiu1999/exchange/internal/infrastructure/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupOutboxIntegration(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:123qwe@localhost:5432/exchange?sslmode=disable"
	}

	pool, err := db.NewPostgresPool(context.Background(), db.DefaultDBConfig(dbURL))
	if err != nil {
		t.Skipf("skip: cannot create pool (%v)", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("skip: db unreachable (%v)", err)
	}

	ensureOutboxSchema(t, pool)
	_, err = pool.Exec(context.Background(), `TRUNCATE outbox_messages`)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `TRUNCATE outbox_messages`)
		pool.Close()
	})

	return NewRepository(pool), pool
}

func ensureOutboxSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS outbox_messages (
			id UUID NOT NULL PRIMARY KEY,
			aggregate_id VARCHAR(64) NOT NULL,
			aggregate_type VARCHAR(32) NOT NULL,
			topic VARCHAR(128) NOT NULL,
			partition_key VARCHAR(64) NOT NULL,
			payload BYTEA NOT NULL,
			status SMALLINT NOT NULL DEFAULT 0,
			retry_count INT NOT NULL DEFAULT 0,
			created_at BIGINT NOT NULL,
			published_at BIGINT NOT NULL DEFAULT 0
		)`)
	require.NoError(t, err)
}

func insertStaleMessage(t *testing.T, repo *Repository, pool *pgxpool.Pool, partitionKey string) *Message {
	t.Helper()

	msg := &Message{
		AggregateID:   partitionKey,
		AggregateType: "order",
		Topic:         "exchange.orders",
		PartitionKey:  partitionKey,
		Payload:       []byte(`{"symbol":"` + partitionKey + `"}`),
	}
	require.NoError(t, repo.Insert(context.Background(), msg))

	_, err := pool.Exec(context.Background(), `
		UPDATE outbox_messages
		SET created_at = $1
		WHERE id = $2`,
		time.Now().Add(-10*time.Second).UnixMilli(), msg.ID,
	)
	require.NoError(t, err)
	return msg
}

type blockingPublisher struct {
	once             sync.Once
	firstCallStarted chan struct{}
	releaseFirstCall chan struct{}
	mu               sync.Mutex
	publishedKeys    []string
}

func newBlockingPublisher() *blockingPublisher {
	return &blockingPublisher{
		firstCallStarted: make(chan struct{}),
		releaseFirstCall: make(chan struct{}),
	}
}

func (p *blockingPublisher) PublishRaw(ctx context.Context, topic, partitionKey string, value []byte) error {
	p.once.Do(func() {
		close(p.firstCallStarted)
		<-p.releaseFirstCall
	})

	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishedKeys = append(p.publishedKeys, partitionKey)
	return nil
}

func (p *blockingPublisher) PublishedKeys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.publishedKeys))
	copy(out, p.publishedKeys)
	return out
}

type failingPublisher struct {
	err error
}

func (p *failingPublisher) PublishRaw(ctx context.Context, topic, partitionKey string, value []byte) error {
	return p.err
}

func TestWorkerProcess_Integration_ConcurrentWorkersPublishEachMessageOnce(t *testing.T) {
	repo, pool := setupOutboxIntegration(t)
	for _, partitionKey := range []string{"BTC-USD", "ETH-USD", "SOL-USD", "XRP-USD", "ADA-USD"} {
		insertStaleMessage(t, repo, pool, partitionKey)
	}

	publisher := newBlockingPublisher()
	worker1 := NewWorker(repo, publisher, time.Second, 5)
	worker2 := NewWorker(repo, publisher, time.Second, 5)

	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		worker1.process(context.Background())
	}()

	select {
	case <-publisher.firstCallStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("worker1 did not start publishing in time")
	}

	worker2.process(context.Background())
	close(publisher.releaseFirstCall)

	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("worker1 did not finish in time")
	}

	published := publisher.PublishedKeys()
	assert.Len(t, published, 5)
	assert.Len(t, uniqueStrings(published), 5)

	pending, err := repo.CountPending(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(0), pending)
}

func TestWorkerProcess_Integration_PublishFailureIncrementsRetryAndKeepsMessage(t *testing.T) {
	repo, pool := setupOutboxIntegration(t)
	msg := insertStaleMessage(t, repo, pool, "BTC-USD")
	worker := NewWorker(repo, &failingPublisher{err: errors.New("kafka unavailable")}, time.Second, 1)

	worker.process(context.Background())

	var retryCount int
	var status int16
	err := pool.QueryRow(context.Background(), `
		SELECT retry_count, status
		FROM outbox_messages
		WHERE id = $1`,
		msg.ID,
	).Scan(&retryCount, &status)
	require.NoError(t, err)
	assert.Equal(t, 1, retryCount)
	assert.Equal(t, StatusPending, Status(status))

	pending, err := repo.CountPending(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), pending)
}

func uniqueStrings(values []string) map[string]struct{} {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		unique[value] = struct{}{}
	}
	return unique
}
