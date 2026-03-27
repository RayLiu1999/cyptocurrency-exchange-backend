/**
 * 場景 A：撮合引擎容量測試（Matching Engine Capacity Test）
 *
 * 目的：找出系統在 BTC-USD 單一交易對下的 TPS 容量拐點。
 * 測試假設：多個打單者持續對同一交易對下限價單，觀察下單吞吐量
 *            與 P95/P99 延遲何時開始退化。
 *
 * 預期結論：
 *   - 低 VU（10-50）時，P95 < 100ms，代表 API 層健康
 *   - 高 VU（100+）時，P95 上升，找出第一個瓶頸層（DB/Kafka/CPU）
 */

import http from "k6/http";
import { check, sleep } from "k6";
import { Rate, Trend, Counter } from "k6/metrics";

// 自訂指標：對標 AXS 的 Custom Metrics 設計
const orderSuccessRate = new Rate("exchange_order_success_rate");
const orderRejectedRate = new Rate("exchange_order_rejected_rate");   // 被限流或餘額不足
const orderP99Latency = new Trend("exchange_order_p99_latency_ms", true);
const orderThroughput = new Counter("exchange_order_total_count");

export const options = {
  stages: [
    { duration: "30s", target: 10 },   // 暖機
    { duration: "1m",  target: 50 },   // 中等負載
    { duration: "1m",  target: 100 },  // 高負載：觀察 P99 是否開始退化
    { duration: "1m",  target: 200 },  // 極限負載：找出第一個瓶頸
    { duration: "30s", target: 0 },
  ],
  thresholds: {
    // 核心 SLA 門檻
    "exchange_order_success_rate":   ["rate>0.95"],  // 95% 成功率（不含 429）
    "http_req_duration":             ["p(95)<500"],  // P95 < 500ms
    "http_req_failed":               ["rate<0.05"],
  },
};

const baseUrl = __ENV.BASE_URL || "http://localhost:8100/api/v1";
const symbol  = __ENV.SYMBOL   || "BTC-USD";

let persistentUserId;

export default function () {
  // 每個 VU 只建立一次帳號
  if (!persistentUserId) {
    const joinRes = http.post(`${baseUrl}/test/join`, null);
    if (joinRes.status === 201) {
      persistentUserId = joinRes.json("user_id");
    } else {
      sleep(1);
      return;
    }
  }

  const side = Math.random() > 0.5 ? "BUY" : "SELL";
  // 買賣單價格集中在 ±200 區間：確保訂單能相互撮合，製造真實成交
  const price = (50000 + (Math.random() * 400 - 200)).toFixed(2).toString();

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
  const rateLimited = res.status === 429;

  orderSuccessRate.add(success);
  orderRejectedRate.add(rateLimited);
  orderP99Latency.add(latency);
  orderThroughput.add(1);

  check(res, {
    // 系統必須優雅回應：要嘛接單、要嘛限流，絕不能 5xx
    "no server error (not 5xx)": (r) => r.status < 500,
    "order accepted or rate-limited": (r) =>
      r.status === 201 || r.status === 202 || r.status === 429,
  });

  sleep(0.05);
}
