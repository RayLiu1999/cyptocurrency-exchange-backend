-- 初始化測試資料

-- 建立測試用戶
INSERT INTO users (id, email, password_hash, created_at, updated_at)
VALUES 
    ('550e8400-e29b-41d4-a716-446655440000', 'test@example.com', '$2a$10$dummy.hash.for.testing', NOW(), NOW())
ON CONFLICT (id) DO NOTHING;

-- 建立測試帳戶（BTC 和 USD）
INSERT INTO accounts (id, user_id, currency, balance, locked, created_at, updated_at)
VALUES 
    (uuid_generate_v4(), '550e8400-e29b-41d4-a716-446655440000', 'BTC', 1.0, 0, NOW(), NOW()),
    (uuid_generate_v4(), '550e8400-e29b-41d4-a716-446655440000', 'USD', 100000.0, 0, NOW(), NOW())
ON CONFLICT (user_id, currency) DO NOTHING;

-- 驗證資料
SELECT 'Users:' as table_name, COUNT(*) as count FROM users
UNION ALL
SELECT 'Accounts:', COUNT(*) FROM accounts
UNION ALL
SELECT 'Orders:', COUNT(*) FROM orders;
