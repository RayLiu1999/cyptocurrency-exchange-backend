-- Enable UUID extension (用於 uuid_generate_v4，雖然新 id 由 Go 產生，留著備用)
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Users table
CREATE TABLE IF NOT EXISTS users (
    id UUID NOT NULL PRIMARY KEY,          -- Go 端使用 UUID v7 產生
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    created_at BIGINT NOT NULL,            -- Unix 毫秒，由 Go 產生
    updated_at BIGINT NOT NULL             -- Unix 毫秒，由 Go 產生
);

-- Accounts (Wallets) table
CREATE TABLE IF NOT EXISTS accounts (
    id UUID NOT NULL PRIMARY KEY,          -- Go 端使用 UUID v7 產生
    user_id UUID NOT NULL REFERENCES users(id),
    currency VARCHAR(10) NOT NULL,
    balance DECIMAL(38, 18) NOT NULL DEFAULT 0,
    locked DECIMAL(38, 18) NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL,            -- Unix 毫秒，由 Go 產生
    updated_at BIGINT NOT NULL,            -- Unix 毫秒，由 Go 產生
    UNIQUE(user_id, currency),
    CHECK (balance >= 0),
    CHECK (locked >= 0)
);

-- Orders table
-- side:   1=BUY, 2=SELL
-- type:   1=LIMIT, 2=MARKET
-- status: 1=NEW, 2=PARTIALLY_FILLED, 3=FILLED, 4=CANCELED, 5=REJECTED
CREATE TABLE IF NOT EXISTS orders (
    id UUID NOT NULL PRIMARY KEY,          -- Go 端使用 UUID v7 產生
    user_id UUID NOT NULL REFERENCES users(id),
    symbol VARCHAR(20) NOT NULL,           -- 例如 "BTC-USD"
    side SMALLINT NOT NULL,               -- 1=BUY, 2=SELL
    type SMALLINT NOT NULL,               -- 1=LIMIT, 2=MARKET
    price DECIMAL(38, 18) NOT NULL,
    quantity DECIMAL(38, 18) NOT NULL,
    filled_quantity DECIMAL(38, 18) NOT NULL DEFAULT 0,
    status SMALLINT NOT NULL,             -- 1=NEW, 2=PARTIALLY_FILLED, 3=FILLED, 4=CANCELED, 5=REJECTED
    created_at BIGINT NOT NULL,            -- Unix 毫秒，由 Go 產生
    updated_at BIGINT NOT NULL             -- Unix 毫秒，由 Go 產生
);

-- Trades table (Execution history)
CREATE TABLE IF NOT EXISTS trades (
    id UUID NOT NULL PRIMARY KEY,          -- Go 端使用 UUID v7 產生
    maker_order_id UUID NOT NULL REFERENCES orders(id),
    taker_order_id UUID NOT NULL REFERENCES orders(id),
    symbol VARCHAR(20) NOT NULL,
    price DECIMAL(38, 18) NOT NULL,
    quantity DECIMAL(38, 18) NOT NULL,
    created_at BIGINT NOT NULL             -- Unix 毫秒，由 Go 產生
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_orders_user_id ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_symbol_side_price ON orders(symbol, side, price); -- 供撮合引擎使用

-- ----------------------------------------------------------
-- Outbox Pattern (訊息可靠傳遞)
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS outbox_messages (
    id              UUID        NOT NULL PRIMARY KEY,    -- UUID v7
    aggregate_id    VARCHAR(64) NOT NULL,                -- 業務 ID
    aggregate_type  VARCHAR(32) NOT NULL,                -- 事件分類
    topic           VARCHAR(128) NOT NULL,               -- 目標 Kafka topic
    partition_key   VARCHAR(64) NOT NULL,                -- Kafka partition key
    payload         BYTEA       NOT NULL,                -- 序列化的事件 payload
    status          SMALLINT    NOT NULL DEFAULT 0,      -- 0=Pending, 1=Published
    retry_count     INT         NOT NULL DEFAULT 0,      -- 已重試次數
    created_at      BIGINT      NOT NULL,                -- 建立時間
    published_at    BIGINT      NOT NULL DEFAULT 0       -- 成功發送時間
);

CREATE INDEX IF NOT EXISTS idx_outbox_messages_status_created_at
    ON outbox_messages (status, created_at)
    WHERE status = 0;

CREATE INDEX IF NOT EXISTS idx_outbox_messages_aggregate_id
    ON outbox_messages (aggregate_id);

-- ----------------------------------------------------------
-- Leader Election (Kafka Partition 選主)
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS partition_leader_locks (
    partition     VARCHAR(128) NOT NULL PRIMARY KEY,  -- Partition 唯一識別
    leader_id     VARCHAR(255) NOT NULL,              -- Leader 實例 ID
    fencing_token BIGINT       NOT NULL DEFAULT 1,    -- 單調遞增防腦裂令牌
    expires_at    BIGINT       NOT NULL               -- 租約到期時間
);

CREATE INDEX IF NOT EXISTS idx_partition_leader_locks_expires_at
    ON partition_leader_locks (expires_at);
