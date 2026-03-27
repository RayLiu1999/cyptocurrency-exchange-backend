-- Outbox Pattern Migration
-- 建立 outbox_messages 表，作為訂單事件的可靠傳遞緩衝區
-- 設計原則：與業務事務（下單/取消訂單）在同一個 DB Transaction 內寫入
-- 確保即使 Kafka 短暫不可用，訊息也不會遺失

CREATE TABLE IF NOT EXISTS outbox_messages (
    id              UUID        NOT NULL PRIMARY KEY,    -- UUID v7（時間序列友好，B-Tree 遞增寫入）
    aggregate_id    VARCHAR(64) NOT NULL,                -- 業務 ID（如 OrderID 或 Symbol）
    aggregate_type  VARCHAR(32) NOT NULL,                -- 事件分類（如 order, cancel_order）
    topic           VARCHAR(128) NOT NULL,               -- 目標 Kafka topic
    partition_key   VARCHAR(64) NOT NULL,                -- Kafka partition key（通常是 symbol）
    payload         BYTEA       NOT NULL,                -- 序列化的事件 payload（JSON）
    status          SMALLINT    NOT NULL DEFAULT 0,      -- 0=Pending, 1=Published
    retry_count     INT         NOT NULL DEFAULT 0,      -- 已重試次數（超過閾值可送 DLQ）
    created_at      BIGINT      NOT NULL,                -- 建立時間（Unix 毫秒）
    published_at    BIGINT      NOT NULL DEFAULT 0       -- 成功發送時間（Unix 毫秒，0 表示未發送）
);

-- 加速 Outbox Worker 掃描 Pending 訊息的查詢效能
CREATE INDEX IF NOT EXISTS idx_outbox_messages_status_created_at
    ON outbox_messages (status, created_at)
    WHERE status = 0;

-- 加速依 aggregate_id 追蹤特定訂單事件的查詢（除錯/稽核用途）
CREATE INDEX IF NOT EXISTS idx_outbox_messages_aggregate_id
    ON outbox_messages (aggregate_id);
