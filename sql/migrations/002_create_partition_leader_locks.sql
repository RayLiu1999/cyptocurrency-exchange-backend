-- Leader Election Migration
-- 建立 partition_leader_locks 表，用於 Kafka Partition 選主機制
-- 設計原則：
--   - 使用 PostgreSQL 的 UPSERT + WHERE 原子條件確保選主互斥
--   - Fencing Token 單調遞增，防止腦裂（舊 Leader 網路恢復後的過期寫入會被 DB 拒絕）
--   - expires_at 控制租約 TTL，Leader 異常下線後租約自然過期，備機自動接管
--
-- 核心競選 SQL 邏輯（in repository.go AcquireLock）：
--   INSERT INTO partition_leader_locks (partition, leader_id, fencing_token, expires_at)
--   VALUES ($1, $2, 1, $3)
--   ON CONFLICT (partition) DO UPDATE
--     SET leader_id = EXCLUDED.leader_id,
--         fencing_token = partition_leader_locks.fencing_token + 1,
--         expires_at = EXCLUDED.expires_at
--     WHERE partition_leader_locks.expires_at < NOW()  -- 只有過期才允許覆蓋
--   RETURNING fencing_token
--
-- Fencing Token 工作原理：
--   1. Leader A 取得 fencing_token = 5，開始處理 Kafka 事件
--   2. A 網路斷線，租約過期
--   3. Leader B 競選成功，取得 fencing_token = 6
--   4. A 網路恢復，嘗試延長租約（ExtendLease with token=5）
--   5. DB 發現 WHERE fencing_token = 5 不符（現在是 6），拒絕更新（0 rows affected）
--   6. A 退回 Standby，腦裂風險解除

CREATE TABLE IF NOT EXISTS partition_leader_locks (
    partition     VARCHAR(128) NOT NULL PRIMARY KEY,  -- Partition 唯一識別（例如 "orders:BTC-USD"）
    leader_id     VARCHAR(255) NOT NULL,              -- Leader 實例 ID（Pod Name 或 UUID）
    fencing_token BIGINT       NOT NULL DEFAULT 1,   -- 單調遞增防腦裂令牌
    expires_at    BIGINT       NOT NULL               -- 租約到期時間（Unix 毫秒）
);

-- 加速依到期時間查詢（Standby 競選時的條件查詢）
CREATE INDEX IF NOT EXISTS idx_partition_leader_locks_expires_at
    ON partition_leader_locks (expires_at);
