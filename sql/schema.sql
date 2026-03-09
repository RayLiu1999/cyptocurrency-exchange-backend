-- Enable UUID extension (用於 uuid_generate_v4，雖然新 id 由 Go 產生，留著備用)
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Users table
CREATE TABLE users (
    id UUID NOT NULL PRIMARY KEY,          -- Go 端使用 UUID v7 產生
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    created_at BIGINT NOT NULL,            -- Unix 毫秒，由 Go 產生
    updated_at BIGINT NOT NULL             -- Unix 毫秒，由 Go 產生
);

-- Accounts (Wallets) table
CREATE TABLE accounts (
    id UUID NOT NULL PRIMARY KEY,          -- Go 端使用 UUID v7 產生
    user_id UUID NOT NULL REFERENCES users(id),
    currency VARCHAR(10) NOT NULL,
    balance DECIMAL(20, 8) NOT NULL DEFAULT 0,
    locked DECIMAL(20, 8) NOT NULL DEFAULT 0,
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
CREATE TABLE orders (
    id UUID NOT NULL PRIMARY KEY,          -- Go 端使用 UUID v7 產生
    user_id UUID NOT NULL REFERENCES users(id),
    symbol VARCHAR(20) NOT NULL,           -- 例如 "BTC-USD"
    side SMALLINT NOT NULL,               -- 1=BUY, 2=SELL
    type SMALLINT NOT NULL,               -- 1=LIMIT, 2=MARKET
    price DECIMAL(20, 8) NOT NULL,
    quantity DECIMAL(20, 8) NOT NULL,
    filled_quantity DECIMAL(20, 8) NOT NULL DEFAULT 0,
    status SMALLINT NOT NULL,             -- 1=NEW, 2=PARTIALLY_FILLED, 3=FILLED, 4=CANCELED, 5=REJECTED
    created_at BIGINT NOT NULL,            -- Unix 毫秒，由 Go 產生
    updated_at BIGINT NOT NULL             -- Unix 毫秒，由 Go 產生
);

-- Trades table (Execution history)
CREATE TABLE trades (
    id UUID NOT NULL PRIMARY KEY,          -- Go 端使用 UUID v7 產生
    maker_order_id UUID NOT NULL REFERENCES orders(id),
    taker_order_id UUID NOT NULL REFERENCES orders(id),
    symbol VARCHAR(20) NOT NULL,
    price DECIMAL(20, 8) NOT NULL,
    quantity DECIMAL(20, 8) NOT NULL,
    created_at BIGINT NOT NULL             -- Unix 毫秒，由 Go 產生
);

-- Indexes for performance
CREATE INDEX idx_orders_user_id ON orders(user_id);
CREATE INDEX idx_orders_status ON orders(status);
CREATE INDEX idx_orders_symbol_side_price ON orders(symbol, side, price); -- 供撮合引擎使用
