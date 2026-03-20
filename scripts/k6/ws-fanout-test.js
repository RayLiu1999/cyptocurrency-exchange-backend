import ws from "k6/ws";
import { check } from "k6";

export const options = {
  stages: [
    { duration: "30s", target: 2000 }, // 在 30 秒內建立 2,000 個 WebSocket 長連線
    { duration: "1m", target: 2000 }, // 頂峰：維持連線 1 分鐘。(此時請同時執行 load-test.js 打單製造行情)
    { duration: "15s", target: 0 }, // 降溫：斷開連線
  ],
};

// 根據架構文件，WebSocket 是接在 Gateway 上的 `/ws` 路徑
const wsUrl = __ENV.WS_URL || "ws://localhost:8100/ws";

export default function () {
  // 注意：這裡的 URL 參數與訂閱 (Subscribe) 格式，請務必按照你 Market-Data 服務真實的設計修改
  const url = `${wsUrl}?topic=orderbook:BTC-USD`;

  const res = ws.connect(url, {}, function (socket) {
    socket.on("open", () => {
      // 如果你的 WebSocket 設計是連線後還要送出 JSON 訂閱指令，請打開下方的註解並修改
      // socket.send(JSON.stringify({ action: "subscribe", channel: "trades", symbol: "BTC-USD" }));
    });

    socket.on("message", (msg) => {
      // 這裡主要目的是讓 2000 個連線持續接收 Market-Data 廣播的訊息，驗證 Go Fanout 效能
      // 你可以印出接收時間來計算推播延遲
      // console.log(`Received message: ${msg}`);
    });

    socket.on("close", () => {
      // 斷線處理
    });

    socket.setTimeout(function () {
      socket.close();
    }, 60000); // 確保測試結束時主動關閉連線
  });

  check(res, {
    "WebSocket connected successfully (101)": (r) => r && r.status === 101,
  });
}
