package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubCacheRepository struct {
	snapshot *matching.OrderBookSnapshot
	err      error
	lastKey  string
	setCalls chan struct{}
}

func (s *stubCacheRepository) GetOrderBookSnapshot(_ context.Context, symbol string) (*matching.OrderBookSnapshot, error) {
	s.lastKey = symbol
	if s.err != nil {
		return nil, s.err
	}
	return s.snapshot, nil
}

func (s *stubCacheRepository) SetOrderBookSnapshot(_ context.Context, snapshot *matching.OrderBookSnapshot) error {
	s.snapshot = snapshot
	if s.setCalls != nil {
		select {
		case s.setCalls <- struct{}{}:
		default:
		}
	}
	return nil
}

type stubEventPublisher struct {
	topic   string
	key     string
	payload any
	called  int
	err     error
}

func (s *stubEventPublisher) Publish(_ context.Context, topic, key string, payload interface{}) error {
	s.called++
	s.topic = topic
	s.key = key
	s.payload = payload
	return s.err
}

func (s *stubEventPublisher) Close() {}

type stubTradeListener struct {
	orderBookSnapshots []*matching.OrderBookSnapshot
	trades             []*matching.Trade
	orders             []*Order
}

func (s *stubTradeListener) OnTrade(trade *matching.Trade) {
	s.trades = append(s.trades, trade)
}

func (s *stubTradeListener) OnOrderUpdate(order *Order) {
	s.orders = append(s.orders, order)
}

func (s *stubTradeListener) OnOrderBookUpdate(snapshot *matching.OrderBookSnapshot) {
	s.orderBookSnapshots = append(s.orderBookSnapshots, snapshot)
}

func TestEstimateMarketBuyFunds_UsesRedisSnapshotInMicroserviceMode(t *testing.T) {
	cacheRepo := &stubCacheRepository{
		snapshot: &matching.OrderBookSnapshot{
			Symbol: "BTC-USD",
			Asks: []matching.OrderBookLevel{
				{Price: decimal.NewFromInt(100), Quantity: decimal.NewFromInt(1)},
				{Price: decimal.NewFromInt(110), Quantity: decimal.NewFromInt(2)},
			},
		},
	}
	svc := NewExchangeService(nil, nil, nil, nil, nil, "BTC-USD", nil, cacheRepo, &stubEventPublisher{})

	funds, err := svc.estimateMarketBuyFunds("BTC-USD", decimal.NewFromFloat(1.5))

	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(155).Equal(funds))
	assert.Equal(t, "BTC-USD", cacheRepo.lastKey)
	assert.Empty(t, svc.engineManager.GetEngine("BTC-USD").GetOrderBookSnapshot(20).Asks)
}

func TestEstimateMarketBuyFunds_FallsBackToEngineWhenRedisMisses(t *testing.T) {
	cacheRepo := &stubCacheRepository{err: errors.New("redis miss")}
	svc := NewExchangeService(nil, nil, nil, nil, nil, "BTC-USD", nil, cacheRepo, &stubEventPublisher{})
	engine := svc.engineManager.GetEngine("BTC-USD")
	engine.Process(matching.NewOrder(uuid.New(), uuid.New(), matching.SideSell, decimal.NewFromInt(100), decimal.NewFromInt(2)))

	funds, err := svc.estimateMarketBuyFunds("BTC-USD", decimal.NewFromInt(1))

	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(100).Equal(funds))
	assert.Equal(t, "BTC-USD", cacheRepo.lastKey)
}

func TestHandleOrderBookEvent_RelaysSnapshotToTradeListener(t *testing.T) {
	listener := &stubTradeListener{}
	svc := NewExchangeService(nil, nil, nil, nil, nil, "BTC-USD", listener, nil, nil)

	event := OrderBookUpdatedEvent{
		EventType: EventOrderBookUpdated,
		Symbol:    "BTC-USD",
		Snapshot: &matching.OrderBookSnapshot{
			Symbol: "BTC-USD",
			Bids: []matching.OrderBookLevel{
				{Price: decimal.NewFromInt(99), Quantity: decimal.NewFromInt(2)},
			},
		},
	}
	payload, err := json.Marshal(event)
	require.NoError(t, err)

	err = svc.HandleOrderBookEvent(context.Background(), []byte("BTC-USD"), payload)

	require.NoError(t, err)
	require.Len(t, listener.orderBookSnapshots, 1)
	assert.Equal(t, "BTC-USD", listener.orderBookSnapshots[0].Symbol)
	assert.Len(t, listener.orderBookSnapshots[0].Bids, 1)
}

func TestHandleTradeEvent_RelaysTradeToTradeListener(t *testing.T) {
	listener := &stubTradeListener{}
	svc := NewExchangeService(nil, nil, nil, nil, nil, "BTC-USD", listener, nil, nil)

	event := TradeExecutedEvent{
		EventType:    EventTradeExecuted,
		Symbol:       "BTC-USD",
		TradeID:      uuid.New(),
		MakerOrderID: uuid.New(),
		TakerOrderID: uuid.New(),
		Price:        decimal.NewFromInt(101),
		Quantity:     decimal.NewFromFloat(0.25),
	}
	payload, err := json.Marshal(event)
	require.NoError(t, err)

	err = svc.HandleTradeEvent(context.Background(), []byte("BTC-USD"), payload)

	require.NoError(t, err)
	require.Len(t, listener.trades, 1)
	assert.Equal(t, event.TradeID, listener.trades[0].ID)
	assert.Equal(t, event.Symbol, listener.trades[0].Symbol)
}

func TestHandleOrderUpdatedEvent_RelaysOrderToTradeListener(t *testing.T) {
	listener := &stubTradeListener{}
	svc := NewExchangeService(nil, nil, nil, nil, nil, "BTC-USD", listener, nil, nil)

	order := &Order{
		ID:             uuid.New(),
		UserID:         uuid.New(),
		Symbol:         "BTC-USD",
		Side:           SideBuy,
		Type:           TypeLimit,
		Price:          decimal.NewFromInt(100),
		Quantity:       decimal.NewFromInt(1),
		FilledQuantity: decimal.NewFromFloat(0.5),
		Status:         StatusPartiallyFilled,
	}
	event := OrderUpdatedEvent{
		EventType: EventOrderUpdated,
		Symbol:    order.Symbol,
		Order:     order,
	}
	payload, err := json.Marshal(event)
	require.NoError(t, err)

	err = svc.HandleOrderUpdatedEvent(context.Background(), []byte(order.Symbol), payload)

	require.NoError(t, err)
	require.Len(t, listener.orders, 1)
	assert.Equal(t, order.ID, listener.orders[0].ID)
	assert.Equal(t, StatusPartiallyFilled, listener.orders[0].Status)
}

func TestOnOrderBookUpdate_PublishesKafkaEventWhenNoTradeListener(t *testing.T) {
	publisher := &stubEventPublisher{}
	cacheRepo := &stubCacheRepository{setCalls: make(chan struct{}, 1)}
	svc := NewExchangeService(nil, nil, nil, nil, nil, "BTC-USD", nil, cacheRepo, publisher)
	snapshot := &matching.OrderBookSnapshot{
		Symbol: "BTC-USD",
		Asks: []matching.OrderBookLevel{
			{Price: decimal.NewFromInt(101), Quantity: decimal.NewFromInt(3)},
		},
	}

	svc.OnOrderBookUpdate(snapshot)

	assert.Equal(t, 1, publisher.called)
	assert.Equal(t, TopicOrderBook, publisher.topic)
	assert.Equal(t, "BTC-USD", publisher.key)
	publishedEvent, ok := publisher.payload.(*OrderBookUpdatedEvent)
	require.True(t, ok)
	assert.Equal(t, EventOrderBookUpdated, publishedEvent.EventType)
	assert.Same(t, snapshot, publishedEvent.Snapshot)
	require.Eventually(t, func() bool {
		return cacheRepo.snapshot != nil
	}, time.Second, 10*time.Millisecond)
	assert.Same(t, snapshot, cacheRepo.snapshot)
}
