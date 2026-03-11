#!/bin/bash
# Redis 功能自動化驗證腳本

SERVER_URL="http://localhost:8080/api/v1"
SYMBOL="BTC-USD"

echo "==========================================="
echo "🔍 1. 測試 OrderBook 快取命中率"
echo "==========================================="
echo "第一次請求 (冷啟動/資料庫)..."
time curl -s "$SERVER_URL/orderbook?symbol=$SYMBOL" > /dev/null
echo ""
echo "第二次請求 (預期從 Redis 讀取)..."
time curl -s "$SERVER_URL/orderbook?symbol=$SYMBOL" > /dev/null
echo ">>> 請觀察 Server Log 是否出現 [Redis Cache] Hit"

echo -e "\n==========================================="
echo "🛡️ 2. 測試 API 限流 (Rate Limit)"
echo "==========================================="
echo "快速發送請求以觸發限流 (預期 60 次後失敗)..."
for i in {1..65}
do
   status=$(curl -s -o /dev/null -w "%{http_code}" "$SERVER_URL/orderbook?symbol=$SYMBOL")
   if [ "$status" == "429" ]; then
      echo "✅ 成功觸發限流！第 $i 次請求回傳 429"
      break
   fi
done

echo -e "\n==========================================="
echo "🔑 3. 測試冪等性 (Idempotency)"
echo "==========================================="
KEY="test-idemp-$(date +%s)"
echo "使用 Key: $KEY 發送第一次請求..."
curl -s -X POST "$SERVER_URL/orders" \
     -H "Content-Type: application/json" \
     -H "Idempotency-Key: $KEY" \
     -d '{
           "symbol": "BTC-USD",
           "side": "BUY",
           "type": "LIMIT",
           "price": "50000",
           "quantity": "0.1"
         }' > /tmp/resp1.json
echo "第一次結果已存至 /tmp/resp1.json"

echo "使用相同 Key 發送第二次請求..."
curl -s -X POST "$SERVER_URL/orders" \
     -H "Content-Type: application/json" \
     -H "Idempotency-Key: $KEY" \
     -d '{
           "symbol": "BTC-USD",
           "side": "BUY",
           "type": "LIMIT",
           "price": "50000",
           "quantity": "0.1"
         }' > /tmp/resp2.json

diff /tmp/resp1.json /tmp/resp2.json
if [ $? -eq 0 ]; then
    echo "✅ 冪等性驗證成功：兩次請求結果完全一致！"
else
    echo "❌ 冪等性驗證失敗：兩次結果不一致"
fi

echo -e "\n==========================================="
echo "🚀 驗證完成！"
echo "==========================================="
