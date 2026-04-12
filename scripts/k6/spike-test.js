/**
 * 【突波防護】Spike Test: 尖峰壓力測試
 * 目的：模擬極端流量瞬間湧入，測試系統的極限與恢復能力。
 *
 * 執行方式：
 *   k6 run spike-test.js
 */
import http from "k6/http";
import { check, sleep } from "k6";

export const options = {
  stages: [
    { duration: "10s", target: 10 }, // 平時負載：維持 10 VU
    { duration: "5s", target: 800 }, // 突發尖峰：大戶砸盤 / 馬斯克發推文，瞬間湧入極端流量
    { duration: "20s", target: 800 }, // 前端雪崩：維持爆量 20 秒
    { duration: "10s", target: 10 }, // 流量退去：瞬間掉回 10 VU
    { duration: "10s", target: 0 }, // 結束壓測
  ],
  thresholds: {
    // 尖峰時期的重點是「不死機」，延遲容忍度可放寬
    http_req_duration: ["p(95)<500"],
  },
};

const baseUrl = __ENV.BASE_URL || "http://localhost:8100/api/v1";
const symbol = __ENV.SYMBOL || "BTC-USD";

// 將 userId 移到函數外部 (Init stage) 以在 VU 迭代間保持持久性
let persistentUserId = __ENV.USER_ID;

export default function () {
  // 在 Spike Test 中，我們確保每個 VU 都有一個合法的帳號
  // 避免使用不存在的 "spike-test-user" 導致測試數據失真
  if (!persistentUserId) {
    const joinRes = http.post(`${baseUrl}/test/join`, null);
    if (joinRes.status === 201) {
      persistentUserId = joinRes.json("user_id");
    } else {
      // 若註冊失敗（可能因為瞬間壓力太大被限流或超時），採用隨機退避 (Jitter)
      // 避免所有 800 個 VU 在同一時間重試，導致再度限流崩潰
      sleep(Math.random() * 2);
      return;
    }
  }

  const userId = persistentUserId;

  // 隨機決定訂單類型與方向，確保系統中有買有賣，避免流動性枯竭
  const isLimit = Math.random() > 0.3; // 70% 是限價單，30% 是市價單
  const side = Math.random() > 0.5 ? "BUY" : "SELL";

  const orderPayload = JSON.stringify({
    user_id: userId,
    symbol: symbol,
    side: side,
    type: isLimit ? "LIMIT" : "MARKET",
    price: isLimit
      ? (50000 + (Math.random() * 200 - 100)).toFixed(2).toString()
      : undefined,
    quantity: "0.01",
  });

  const params = {
    headers: { "Content-Type": "application/json" },
  };

  let res = http.post(`${baseUrl}/orders`, orderPayload, params);

  // === 資金循環 (Recharge) 機制 ===
  if (res.status === 400) {
    const rechargeRes = http.post(`${baseUrl}/test/recharge/${userId}`, null);
    if (rechargeRes.status === 200) {
      res = http.post(`${baseUrl}/orders`, orderPayload, params);
    }
  }

  // 系統必須優雅處理：要嘛進 Kafka 撮合 (201/202)，要嘛被 Redis 限流擋下 (429)
  // 絕對不能出現 500 (Internal Server Error)
  check(res, {
    "handled gracefully (201 or 429)": (r) =>
      r.status === 201 || r.status === 202 || r.status === 429,
    "hit rate limit (429)": (r) => r.status === 429,
  });

  // 惡意流量不等待
  sleep(0.01);
}
