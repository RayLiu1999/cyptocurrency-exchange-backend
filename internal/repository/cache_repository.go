package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/redis"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
)

// RedisCacheRepository 實作 core.CacheRepository 介面 (Redis 版本)
type RedisCacheRepository struct {
	client *redis.Client
}

// NewRedisCacheRepository 建立 Redis 快取實作
func NewRedisCacheRepository(client *redis.Client) domain.CacheRepository {
	return &RedisCacheRepository{
		client: client,
	}
}

// GetOrderBookSnapshot 從 Redis 讀取指定交易對的訂單簿快照
func (r *RedisCacheRepository) GetOrderBookSnapshot(ctx context.Context, symbol string) (*engine.OrderBookSnapshot, error) {
	key := fmt.Sprintf("exchange:orderbook:%s", symbol)

	// 從 Redis 取得 JSON 字串
	data, err := r.client.Client.Get(ctx, key).Bytes()
	if err != nil {
		// redis.Nil 代表 Key 不存在 (Cache Miss)
		return nil, err
	}

	// 反序列化 JSON 回 OrderBookSnapshot
	var snapshot engine.OrderBookSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("反序列化快取失敗: %w", err)
	}

	return &snapshot, nil
}

// SetOrderBookSnapshot 將訂單簿快照寫入 Redis。
// 這裡刻意不設定 TTL，因為 market-data-service 的讀路徑以 Redis 為唯一快取來源；
// 若靜態掛單簿在無交易期間過期，對外查詢會誤判成空盤。
func (r *RedisCacheRepository) SetOrderBookSnapshot(ctx context.Context, snapshot *engine.OrderBookSnapshot) error {
	key := fmt.Sprintf("exchange:orderbook:%s", snapshot.Symbol)

	// 序列化成 JSON
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("序列化快取失敗: %w", err)
	}

	// Redis Lua 腳本：LWW (Last Write Wins) 透過 FencingToken 比較
	// 解析現有 JSON 快照中的 fencing_token，若傳入的 token 比較小，則拒絕寫入。
	// 這保護了前端畫面不會因為殭屍機器的延遲快照而閃爍（幽靈掛單）。
	const luaScript = `
	local current_val = redis.call('GET', KEYS[1])
	if current_val then
		-- 使用 cjson 安全解碼，即使是舊版（沒有 fencing_token），預設為 0
		local success, decoded = pcall(cjson.decode, current_val)
		if success and type(decoded) == "table" then
			local current_token = tonumber(decoded.fencing_token) or 0
			local new_token = tonumber(ARGV[1]) or 0
			if new_token > 0 and new_token < current_token then
				return 0 -- Stale token, 拒絕寫入
			end
		end
	end
	redis.call('SET', KEYS[1], ARGV[2])
	return 1
	`

	res, err := r.client.Client.Eval(ctx, luaScript, []string{key}, snapshot.FencingToken, data).Result()
	if err != nil {
		return fmt.Errorf("寫入 Redis 快照腳本失敗: %w", err)
	}

	if res.(int64) == 0 {
		return fmt.Errorf("殭屍快照攔截：Token %d 小於快取中版本，已拒絕寫入大盤畫面", snapshot.FencingToken)
	}

	return nil
}
