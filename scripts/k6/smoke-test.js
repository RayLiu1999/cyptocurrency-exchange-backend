/**
 * 【基礎驗證】Smoke Test: 端到端邏輯驗證（Smoke Test / E2E Sanity Check）
 * 目的：確保核心業務流程健康，包含註冊、入金、下單、查單與撤單。
 * 這是 CI/CD 的第一道防線，主要確保系統微服務間的狀態轉換正常，不追求極限 QPS。
 *
 * 執行方式：
 *   k6 run smoke-test.js
 */
import http from "k6/http";
import { check, fail } from "k6";

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: {
    checks: ["rate>0.95"],
    http_req_failed: ["rate<0.05"],
    http_req_duration: ["p(95)<1500"],
  },
};

const baseUrl = __ENV.BASE_URL || "http://localhost:8100/api/v1";
const symbol = __ENV.SYMBOL || "BTC-USD";

function jsonHeaders(extraHeaders = {}) {
  return {
    headers: {
      "Content-Type": "application/json",
      ...extraHeaders,
    },
  };
}

function mustParseJson(response, label) {
  const data = response.json();
  if (!data) {
    fail(`${label} 回應不是有效 JSON`);
  }
  return data;
}

export default function () {
  const joinResponse = http.post(`${baseUrl}/test/join`, null);
  check(joinResponse, {
    "join arena returns 201": (response) => response.status === 201,
  });

  const joinData = mustParseJson(joinResponse, "JoinArena");
  const userId = joinData.user_id;
  if (!userId) {
    fail("JoinArena 未返回 user_id");
  }

  const balancesResponse = http.get(`${baseUrl}/accounts?user_id=${userId}`);
  check(balancesResponse, {
    "get balances returns 200": (response) => response.status === 200,
    "get balances returns account array": (response) =>
      Array.isArray(response.json()),
  });

  const orderBookResponse = http.get(`${baseUrl}/orderbook?symbol=${symbol}`);
  check(orderBookResponse, {
    "get orderbook returns 200": (response) => response.status === 200,
  });

  const orderPayload = JSON.stringify({
    user_id: userId,
    symbol,
    side: "BUY",
    type: "LIMIT",
    price: "50000",
    quantity: "0.01",
  });

  const placeOrderResponse = http.post(
    `${baseUrl}/orders`,
    orderPayload,
    jsonHeaders(),
  );
  check(placeOrderResponse, {
    "place order returns 201 or 202": (response) =>
      response.status === 201 || response.status === 202,
  });

  const placeOrderData = mustParseJson(placeOrderResponse, "PlaceOrder");
  const orderId = placeOrderData.id || placeOrderData.order_id;
  if (!orderId) {
    fail(`PlaceOrder 未返回 order id: ${JSON.stringify(placeOrderData)}`);
  }

  const getOrderResponse = http.get(`${baseUrl}/orders/${orderId}`);
  check(getOrderResponse, {
    "get order returns 200": (response) => response.status === 200,
    "get order returns same id": (response) => response.json("id") === orderId,
    "get order returns NEW status": (response) =>
      response.json("status") === "NEW",
  });

  const listOrdersResponse = http.get(`${baseUrl}/orders?user_id=${userId}`);
  check(listOrdersResponse, {
    "list orders returns 200": (response) => response.status === 200,
    "list orders returns array": (response) => Array.isArray(response.json()),
    "list orders contains created order": (response) =>
      response.json().some((order) => order.id === orderId),
  });

  const cancelOrderResponse = http.del(
    `${baseUrl}/orders/${orderId}?user_id=${userId}`,
    null,
    jsonHeaders({ "X-User-ID": userId }),
  );
  check(cancelOrderResponse, {
    "cancel order returns 200": (response) => response.status === 200,
  });
}
