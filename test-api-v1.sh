#!/bin/bash
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

BASE_URL="http://localhost:8080/api/v1"
USER_ID="21ae8cc8-b42f-48e1-9067-3b1032c4e3ba"

echo -e "${YELLOW}🧪 Exchange API v1 測試腳本${NC}\n"

# 1. 測試 取得訂單簿 (OrderBook)
echo -e "${YELLOW}📝 測試 1: 取得訂單簿 (BTC-USD)${NC}"
curl -s -X GET "$BASE_URL/orderbook?symbol=BTC-USD" | jq .
echo -e "\n"

# 2. 測試 取得帳戶餘額
echo -e "${YELLOW}📝 測試 2: 取得帳戶餘額${NC}"
curl -s -X GET "$BASE_URL/accounts?user_id=$USER_ID" | jq .
echo -e "\n"

# 3. 測試 下限價買單
echo -e "${YELLOW}📝 測試 3: 下限價買單 (BUY BTC-USD)${NC}"
ORDER_RES=$(curl -s -X POST "$BASE_URL/orders" \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "'"$USER_ID"'",
    "symbol": "BTC-USD",
    "side": "BUY",
    "type": "LIMIT",
    "price": "50000",
    "quantity": "0.01"
  }')
echo "$ORDER_RES" | jq .
ORDER_ID=$(echo $ORDER_RES | jq -r '.id')
echo -e "\n"

# 4. 測試 取得單一訂單
if [ "$ORDER_ID" != "null" ]; then
  echo -e "${YELLOW}📝 測試 4: 取得剛下的訂單 ($ORDER_ID)${NC}"
  curl -s -X GET "$BASE_URL/orders/$ORDER_ID" | jq .
  echo -e "\n"
fi

# 5. 測試 取得歷史成交記錄 (Trades)
echo -e "${YELLOW}📝 測試 5: 取得最近成交記錄${NC}"
curl -s -X GET "$BASE_URL/trades?symbol=BTC-USD" | jq .
echo -e "\n"

# 6. 測試 K 線數據
echo -e "${YELLOW}📝 測試 6: 取得 K 線數據 (1m)${NC}"
curl -s -X GET "$BASE_URL/klines?symbol=BTC-USD&interval=1m" | jq .
echo -e "\n"

echo -e "${GREEN}測試完成${NC}"
