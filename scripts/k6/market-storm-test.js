/**
 * 【行情風暴】Storm Test: 行情風暴測試與資源隔離驗證
 *
 * 目的：同時壓測「下單」與「WebSocket 廣播」，
 *       驗證 Market Data Service 的資源隔離是否有效。
 *
 * 設計說明：
 *   - 使用 k6 的 scenarios 功能，讓兩種流量同時跑在同一個測試中
 *   - scenario A（orderers）：模擬 50 個用戶持續下單，製造行情變化
 *   - scenario B（watchers）：模擬 1000 個用戶長期持有 WebSocket 連線
 *
 * 預期結論：
 *   "在 1,000 個 WebSocket 連線 + 同時的下單壓力下，
 *    訂單 P95 延遲沒有因為 WebSocket 廣播而顯著上升，
 *    驗證了 Market Data Service 的資源隔離設計。"
 *
 * 自訂指標：
 *   - exchange_ws_connect_success_rate: WebSocket 連線成功率
 *   - exchange_ws_messages_received: 廣播訊息接收總數（推算推播吞吐量）
 *   - exchange_order_latency_ms: 在廣播壓力下的下單延遲
 */

import http from "k6/http";
import ws   from "k6/ws";
import { check, sleep } from "k6";
import { Rate, Trend, Counter } from "k6/metrics";

const wsConnectRate      = new Rate("exchange_ws_connect_success_rate");
const wsMessagesReceived = new Counter("exchange_ws_messages_received");
const orderLatency       = new Trend("exchange_order_latency_ms", true);
const orderSuccessRate   = new Rate("exchange_order_success_rate");

export const options = {
  scenarios: {
    // 場景 A：下單壓力（製造行情）
    orderers: {
      executor:        "constant-arrival-rate",
      rate:            200,      // 每秒發送 200 單
      timeUnit:        "1s",
      duration:        "2m",
      preAllocatedVUs: 50,
      maxVUs:          150,
      exec:            "sendOrders",
    },
    // 場景 B：WebSocket 廣播觀察者
    watchers: {
      executor:        "ramping-vus",
      exec:            "watchMarket",
      startVUs:        0,
      stages: [
        { duration: "30s", target: 1000 }, // 爬升到 1000 個連線
        { duration: "1m",  target: 1000 }, // 頂峰：維持
        { duration: "30s", target: 0 },
      ],
      gracefulRampDown: "10s",
    },
  },
  thresholds: {
    "exchange_ws_connect_success_rate": ["rate>0.99"],     // 99%+ 連線成功
    "exchange_order_success_rate":      ["rate>0.90"],
    "exchange_order_latency_ms":        ["p(95)<300"],     // 廣播壓力下，下單延遲不應退化
  },
};

const baseUrl = __ENV.BASE_URL || "http://localhost:8100/api/v1";
const wsUrl   = __ENV.WS_URL   || "ws://localhost:8100/ws";
const symbol  = "BTC-USD";

// --- 下單函數 ---
let ordererUserId;

export function sendOrders() {
  if (!ordererUserId) {
    const joinRes = http.post(`${baseUrl}/test/join`, null);
    if (joinRes.status === 201) {
      ordererUserId = joinRes.json("user_id");
    } else {
      sleep(1);
      return;
    }
  }

  const side  = Math.random() > 0.5 ? "BUY" : "SELL";
  const price = (50000 + (Math.random() * 400 - 200)).toFixed(2).toString();

  const payload = JSON.stringify({
    user_id:  ordererUserId,
    symbol:   symbol,
    side:     side,
    type:     "LIMIT",
    price:    price,
    quantity: "0.01",
  });

  const params = { headers: { "Content-Type": "application/json" } };

  let start = Date.now();
  let res = http.post(`${baseUrl}/orders`, payload, params);

  // === 資金循環 (Recharge) 機制 ===
  if (res.status === 400) {
    const rechargeRes = http.post(`${baseUrl}/test/recharge/${ordererUserId}`, null);
    if (rechargeRes.status === 200) {
      start = Date.now();
      res = http.post(`${baseUrl}/orders`, payload, params);
    }
  }

  const latency = Date.now() - start;

  orderLatency.add(latency);
  orderSuccessRate.add(res.status === 201 || res.status === 202);

  check(res, { "order: no 5xx": (r) => r.status < 500 });
}

// --- WebSocket 廣播監聽函數 ---
export function watchMarket() {
  const url = `${wsUrl}?topic=orderbook:${symbol}`;

  const res = ws.connect(url, {}, function (socket) {
    socket.on("message", () => {
      // 每收到一筆廣播訊息就計數，推算廣播吞吐量
      wsMessagesReceived.add(1);
    });

    // 維持連線 90 秒後主動關閉
    socket.setTimeout(() => { socket.close(); }, 90000);
  });

  wsConnectRate.add(res && res.status === 101);

  check(res, { "ws: connected (101)": (r) => r && r.status === 101 });
}
