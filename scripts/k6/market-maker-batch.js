import http from "k6/http";
import { check } from "k6";

export const options = {
  scenarios: {
    batch_makers: {
      executor: "constant-vus",
      vus: 10, // 10 個造市商併發
      duration: "30s", // 測試 30 秒
    },
  },
};

const BATCH_SIZE = 100; // 每個請求包含 100 筆訂單
const baseUrl = __ENV.BASE_URL || "http://localhost:8100/api/v1";

let persistentUserId;

export default function () {
  // 自動註冊並獲取測試用的 User ID
  if (!persistentUserId) {
    const joinRes = http.post(`${baseUrl}/test/join`, null);
    if (joinRes.status === 201) {
      persistentUserId = joinRes.json("user_id");
    } else {
      console.error("無法註冊測試帳戶");
      // 若失敗直接返回，這樣就不會執行後續導致 100% 失敗
      return;
    }
  }

  const orders = [];
  for (let i = 0; i < BATCH_SIZE; i++) {
    const side = Math.random() > 0.5 ? "BUY" : "SELL";
    const price = (49000 + Math.random() * 2000).toFixed(2);

    orders.push({
      user_id: persistentUserId,
      symbol: "BTC-USD",
      side: side,
      type: "LIMIT",
      price: parseFloat(price),
      quantity: 0.1,
    });
  }

  const payload = JSON.stringify(orders);
  const params = {
    headers: {
      "Content-Type": "application/json",
    },
  };

  let res = http.post(`${baseUrl}/orders/batch`, payload, params);

  // === 資金循環 (Recharge) 機制 ===
  // 如果因為該用戶的錢被鎖光而遇到餘額不足錯誤，則充值再試一次
  if (res.status >= 400 && res.body.includes("鎖定期資金失敗")) {
    const rechargeRes = http.post(
      `${baseUrl}/test/recharge/${persistentUserId}`,
      null,
    );
    if (rechargeRes.status === 200) {
      res = http.post(`${baseUrl}/orders/batch`, payload, params);
    }
  }

  check(res, {
    "status is 202": (r) => r.status === 202,
  });
}
