/**
 * 【常態負載】Load Test: 散戶高頻發送負載測試
 * 目的：打擊單張下單 API，驗證 API Gateway、連線池與微服務在常態壓力下的穩定度。
 * 測量系統能同時支撐多少名虛擬用戶 (VU) 執行高頻交易操作。
 *
 * 執行方式：
 *   k6 run load-test.js
 */
import http from "k6/http";
import { check, sleep } from "k6";

export const options = {
  stages: [
    { duration: "30s", target: 100 }, // 暖機：30秒內爬升到 100 個虛擬用戶 (VU)
    { duration: "2m", target: 100 }, // 恆定壓測：維持 100 VU 狂打 2 分鐘
    { duration: "30s", target: 0 }, // 結束：30秒內降回 0
  ],
  thresholds: {
    // 效能目標：95% 的請求必須小於 100ms
    http_req_duration: ["p(95)<100"],
    // 可用性目標：失敗率必須小於 1%
    http_req_failed: ["rate<0.01"],
  },
};

// 注意：壓測時建議打 Gateway 才能完整測到 Rate Limit 與跨微服務鏈路的效能
const baseUrl = __ENV.BASE_URL || "http://localhost:8100/api/v1";
const symbol = __ENV.SYMBOL || "BTC-USD";

// 將 userId 移到函數外部 (Init stage)
// 這樣同一個 VU (Virtual User) 在重複執行迭代時，能保留已經註冊好的 ID
let persistentUserId = __ENV.USER_ID;

export default function () {
  // 每個 VU 獨立註冊一次帳號供後續迴圈使用，避免每次 Post Order 都創帳號拖垮效能
  if (!persistentUserId) {
    const joinRes = http.post(`${baseUrl}/test/join`, null);
    if (joinRes.status === 201) {
      persistentUserId = joinRes.json("user_id");
    } else {
      sleep(1);
      return; // 若註冊失敗則退回重試
    }
  }

  const userId = persistentUserId;

  // 隨機生成買賣限價單，製造深度的同時避免過快相互抵銷
  const payload = JSON.stringify({
    user_id: userId,
    symbol: symbol,
    side: Math.random() > 0.5 ? "BUY" : "SELL",
    type: "LIMIT",
    price: (50000 + (Math.random() * 1000 - 500)).toFixed(2).toString(),
    quantity: "0.01",
  });

  const params = {
    headers: { "Content-Type": "application/json" },
  };

  let res = http.post(`${baseUrl}/orders`, payload, params);

  // === 資金循環 (Recharge) 機制 ===
  // 如果收到 400 Bad Request，極大可能是餘額不足（因為一直下單又沒完全抵銷）。
  // 為了讓壓測能「真正跑到最後」而不被餘額卡的假性瓶頸阻擋，我們動態充值。
  if (res.status >= 400) {
    const rechargeRes = http.post(`${baseUrl}/test/recharge/${userId}`, null);
    if (rechargeRes.status === 200) {
      // 充值成功後再重送一次該筆訂單
      res = http.post(`${baseUrl}/orders`, payload, params);
    }
  }

  // 檢查引擎是否正常接單 (PostgreSQL 與 Kafka 寫入是否健康)
  check(res, {
    "order created (201 or 202)": (r) => r.status === 201 || r.status === 202,
  });

  // 模擬真實用戶操作節奏 (如果你的系統極限很高，可以把這個值調小至 0.01)
  sleep(0.1);
}
