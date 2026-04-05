import http from "k6/http";
import ws from "k6/ws";
import { check, sleep } from "k6";
import { Trend, Rate, Counter } from "k6/metrics";

// 自訂指標：完全對標 AXS 專案的核心指標
const e2eLatency = new Trend("exchange_e2e_latency_ms", true);
const orderSuccessRate = new Rate("exchange_order_success_rate");
const wsMessagesReceived = new Counter("exchange_ws_messages_received");

export const options = {
  scenarios: {
    // 場景 A：下單產生流量
    orderers: {
      executor: "constant-vus",
      vus: 10,
      duration: "1m",
      exec: "sendOrders",
    },
    // 場景 B：WebSocket 監聽端到端延遲
    watcher: {
      executor: "constant-vus",
      vus: 1, // 只需要 1 個消費者負責接收所有推播並計算延遲
      duration: "1m",
      exec: "watchMarket",
    },
  },
  thresholds: {
    "exchange_order_success_rate": ["rate>0.95"],
    "exchange_e2e_latency_ms": ["p(95)<200"], // 期望 P95 的端到端延遲在 200ms 以下
  },
};

const baseUrl = __ENV.BASE_URL || "http://localhost:8100/api/v1";
const wsUrl = __ENV.WS_URL || "ws://localhost:8100/ws";

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

  const payload = JSON.stringify({
    user_id: ordererUserId,
    symbol: "BTC-USD",
    side: Math.random() > 0.5 ? "BUY" : "SELL",
    type: "MARKET", // 使用市價單保證產生交易與推播
    quantity: "0.01",
  });

  const params = { headers: { "Content-Type": "application/json" } };
  
  let res = http.post(`${baseUrl}/orders`, payload, params);

  // === 資金循環 (Recharge) 機制 ===
  if (res.status === 400) {
    const rechargeRes = http.post(`${baseUrl}/test/recharge/${ordererUserId}`, null);
    if (rechargeRes.status === 200) {
      res = http.post(`${baseUrl}/orders`, payload, params);
    }
  }

  const success = res.status === 201 || res.status === 202;
  orderSuccessRate.add(success);

  check(res, { "order: accepted": (r) => r.status === 201 || r.status === 202 });
  
  // 避免將本機 CPU 打滿
  sleep(0.05);
}

export function watchMarket() {
  const res = ws.connect(wsUrl, {}, function (socket) {
    socket.on("message", (msg) => {
      wsMessagesReceived.add(1);
      
      try {
        const payload = JSON.parse(msg);
        
        // 捕捉 order_update 推播事件
        if (payload.type === "order_update") {
          const order = payload.data;
          
          // 計算端到端延遲 (End-to-End Latency)
          // T1: 訂單在 DB 建立的時間 (created_at)
          // T2: 經歷 DB -> Outbox -> Kafka -> Matching Engine -> Kafka -> WS Server -> K6 (Date.now())
          // 註: 前提是 K6 與 Server 執行在同一個實體主機上 (無時鐘偏移)
          const createdAt = new Date(order.created_at).getTime();
          const now = Date.now();
          const latency = now - createdAt;
          
          if (latency > 0 && latency < 5000) { // 濾掉極端異常值
            e2eLatency.add(latency);
          }
        }
      } catch (e) {
        // 忽略解析錯誤
      }
    });

    socket.setTimeout(() => { socket.close(); }, 60000);
  });

  check(res, { "ws: connected": (r) => r && r.status === 101 });
}
