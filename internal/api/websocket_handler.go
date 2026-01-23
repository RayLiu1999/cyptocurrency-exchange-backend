package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/RayLiu1999/exchange/internal/core/matching"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
)

// WebSocketHandler 負責管理 WebSocket 連線與廣播
type WebSocketHandler struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan []byte
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mu         sync.Mutex
}

func NewWebSocketHandler() *WebSocketHandler {
	return &WebSocketHandler{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
	}
}

// Ensure implementation
// var _ core.TradeEventListener = (*WebSocketHandler)(nil)
// (需解決 core import cycle，暫時透過 duck typing 或調整 package 結構，
// 這裡簡單起見我們讓 `HandleWS` 和 `OnTrade` 在同一個 package 即可，
// 但為了依賴反轉，main 裡會檢查 interface。)

// OnTrade 實作 TradeEventListener 介面
func (h *WebSocketHandler) OnTrade(trade *matching.Trade) {
	// 轉換為 JSON 訊息
	msg := map[string]interface{}{
		"type": "trade",
		"data": map[string]interface{}{
			"id":             trade.ID,
			"symbol":         trade.Symbol,
			"price":          trade.Price,
			"quantity":       trade.Quantity,
			"maker_order_id": trade.MakerOrderID,
			"taker_order_id": trade.TakerOrderID,
			"timestamp":      time.Now(),
		},
	}

	jsonMsg, err := json.Marshal(msg)
	if err != nil {
		log.Printf("JSON Marshal Error: %v", err)
		return
	}

	h.Broadcast(jsonMsg)
}

// Run 啟動 WS 管理協程
func (h *WebSocketHandler) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.Close()
			}
			h.mu.Unlock()
		case message := <-h.broadcast:
			h.mu.Lock()
			for client := range h.clients {
				// TODO: 每個 Client 應該要有自己的 Write Loop (避免 Head-of-Line Blocking)
				client.SetWriteDeadline(time.Now().Add(writeWait))
				err := client.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					log.Printf("WS Write Error: %v", err)
					client.Close()
					delete(h.clients, client)
				}
			}
			h.mu.Unlock()
		}
	}
}

// Broadcast 發送訊息給所有客戶端
func (h *WebSocketHandler) Broadcast(message []byte) {
	h.broadcast <- message
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // 允許所有來源 (開發用)
	},
}

// HandleWS 處理 WS 連線請求
func (h *WebSocketHandler) HandleWS(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WS Upgrade Failed: %v", err)
		return
	}

	h.register <- conn

	// 啟動 Ping/Pong 機制
	go h.writePump(conn)
	go h.readPump(conn)
}

// readPump 處理讀取與 Pong
func (h *WebSocketHandler) readPump(conn *websocket.Conn) {
	defer func() {
		h.unregister <- conn
	}()

	conn.SetReadLimit(512)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WS Error: %v", err)
			}
			break
		}
	}
}

// writePump 處理 Pings (目前 Broadcast 還是走 Sync map，未來應移到這裡)
func (h *WebSocketHandler) writePump(conn *websocket.Conn) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		// conn.Close() 由 readPump 或 unregister 負責
	}()

	for range ticker.C {
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
			return
		}
	}
}
