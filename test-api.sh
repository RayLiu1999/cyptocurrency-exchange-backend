#!/bin/bash

# 顏色定義
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

BASE_URL="http://localhost:8080"

echo -e "${YELLOW}🧪 Exchange API 測試腳本${NC}\n"

# 測試 1: 建立測試帳戶
echo -e "${YELLOW}📝 測試 1: 建立測試用戶與帳戶${NC}"
USER_ID="550e8400-e29b-41d4-a716-446655440000"
echo "測試用 User ID: $USER_ID"

# 初始化帳戶（需要先手動在資料庫建立）
echo -e "${GREEN}註：實際應用需先實作註冊 API，這裡假設帳戶已存在${NC}\n"

# 測試 2: 下限價買單
echo -e "${YELLOW}📝 測試 2: 下限價買單 (BUY BTCUSD)${NC}"
RESPONSE=$(curl -s -X POST "$BASE_URL/orders" \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "'"$USER_ID"'",
    "symbol": "BTCUSD",
    "side": "BUY",
    "type": "LIMIT",
    "price": "50000.00",
    "quantity": "0.1"
  }')

if [ $? -eq 0 ]; then
  echo -e "${GREEN}✅ 請求成功${NC}"
  echo "回應: $RESPONSE"
else
  echo -e "${RED}❌ 請求失敗${NC}"
fi
echo ""

# 測試 3: 下限價賣單
echo -e "${YELLOW}📝 測試 3: 下限價賣單 (SELL BTCUSD)${NC}"
RESPONSE=$(curl -s -X POST "$BASE_URL/orders" \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "'"$USER_ID"'",
    "symbol": "BTCUSD",
    "side": "SELL",
    "type": "LIMIT",
    "price": "51000.00",
    "quantity": "0.05"
  }')

if [ $? -eq 0 ]; then
  echo -e "${GREEN}✅ 請求成功${NC}"
  echo "回應: $RESPONSE"
else
  echo -e "${RED}❌ 請求失敗${NC}"
fi
echo ""

# 測試 4: 錯誤案例 - 無效的 User ID
echo -e "${YELLOW}📝 測試 4: 錯誤案例 - 無效的 User ID${NC}"
RESPONSE=$(curl -s -X POST "$BASE_URL/orders" \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "invalid-uuid",
    "symbol": "BTCUSD",
    "side": "BUY",
    "type": "LIMIT",
    "price": "50000.00",
    "quantity": "0.1"
  }')

if [ $? -eq 0 ]; then
  echo -e "${GREEN}✅ 成功捕捉錯誤${NC}"
  echo "回應: $RESPONSE"
else
  echo -e "${RED}❌ 請求失敗${NC}"
fi
echo ""

echo -e "${GREEN}🎉 測試完成！${NC}"
echo -e "${YELLOW}💡 提示：實際測試前需要先在資料庫建立用戶和帳戶，並確保有足夠餘額${NC}"
