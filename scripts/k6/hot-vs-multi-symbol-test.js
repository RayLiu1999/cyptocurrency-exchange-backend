/**
 * 場景 B：熱門交易對 vs 多交易對吞吐量對比
 * （Hot Symbol vs Multi-Symbol Throughput Comparison）
 *
 * 目的：展示 Kafka Partition 設計帶來的影響。
 *   - 所有流量打同一個 Symbol（BTC-USD）→ 單一 Kafka Partition 競爭
 *   - 流量分散到多個 Symbol → 多 Partition 並行，吞吐提升
 *
 * 預期結論：
 *   "多交易對的 P95 比單交易對低 XX%，驗證了我們的 Partition Key=Symbol 設計
 *    能有效做到流量分散，避免單一熱門交易對成為全系統瓶頸。"
 *
 * 執行方式（分兩次跑，截圖對比）：
 *   SYMBOL_MODE=hot  k6 run hot-vs-multi-symbol-test.js  # 單一熱門交易對
 *   SYMBOL_MODE=multi k6 run hot-vs-multi-symbol-test.js # 多交易對
 */

import http from "k6/http";
import { check, sleep } from "k6";
import { Rate, Trend, Counter } from "k6/metrics";

const orderLatency       = new Trend("exchange_order_latency_ms", true);
const orderSuccessRate   = new Rate("exchange_order_success_rate");
const totalOrderCount    = new Counter("exchange_total_orders");

// 支援的多交易對，測試 multi 模式時會隨機選一個（分散 Kafka Partition）
const SYMBOLS = ["BTC-USD", "ETH-USD", "SOL-USD", "BNB-USD", "XRP-USD"];

export const options = {
  vus: 100,
  duration: "2m",
  thresholds: {
    "exchange_order_success_rate": ["rate>0.9"],
    "exchange_order_latency_ms":   ["p(95)<300", "p(99)<1000"],
  },
};

const baseUrl    = __ENV.BASE_URL    || "http://localhost:8100/api/v1";
const symbolMode = __ENV.SYMBOL_MODE || "hot"; // "hot" | "multi"

let persistentUserId;

function pickSymbol() {
  if (symbolMode === "multi") {
    return SYMBOLS[Math.floor(Math.random() * SYMBOLS.length)];
  }
  return "BTC-USD"; // 熱門模式：全部集中在同一個交易對
}

export default function () {
  if (!persistentUserId) {
    const joinRes = http.post(`${baseUrl}/test/join`, null);
    if (joinRes.status === 201) {
      persistentUserId = joinRes.json("user_id");
    } else {
      sleep(1);
      return;
    }
  }

  const symbol = pickSymbol();
  const side   = Math.random() > 0.5 ? "BUY" : "SELL";
  const price  = (50000 + (Math.random() * 400 - 200)).toFixed(2).toString();

  const payload = JSON.stringify({
    user_id:  persistentUserId,
    symbol:   symbol,
    side:     side,
    type:     "LIMIT",
    price:    price,
    quantity: "0.01",
  });

  const start = Date.now();
  const res = http.post(`${baseUrl}/orders`, payload, {
    headers: { "Content-Type": "application/json" },
  });
  const latency = Date.now() - start;

  const success = res.status === 201 || res.status === 202;
  orderLatency.add(latency);
  orderSuccessRate.add(success);
  totalOrderCount.add(1);

  check(res, {
    "no 5xx server error": (r) => r.status < 500,
  });

  sleep(0.02);
}
