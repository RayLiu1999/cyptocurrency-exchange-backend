package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/RayLiu1999/exchange/internal/domain"
	"github.com/RayLiu1999/exchange/internal/infrastructure/metrics"
	"github.com/RayLiu1999/exchange/internal/marketdata"
	"github.com/RayLiu1999/exchange/internal/matching/engine"
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

// Client 封裝 WebSocket 連線與其專屬的寫入緩衝區
type Client struct {
	handler *WebSocketHandler
	conn    *websocket.Conn
	send    chan []byte // 每個 Client 專屬的輸出通道
}

type outboundMessage struct {
	payload     []byte
	messageType string
}

// WebSocketHandler 負責管理 WebSocket 連線與廣播
type WebSocketHandler struct {
	serviceName string
	clients     map[*Client]bool
	broadcast   chan outboundMessage
	register    chan *Client
	unregister  chan *Client
}

func NewWebSocketHandler(serviceName string) *WebSocketHandler {
	return &WebSocketHandler{
		serviceName: serviceName,
		clients:     make(map[*Client]bool),
		broadcast:   make(chan outboundMessage, 256),
		register:    make(chan *Client),
		unregister:  make(chan *Client),
	}
}

// Ensure implementation
var _ marketdata.MarketDataPublisher = (*WebSocketHandler)(nil)

// OnOrderBookUpdate 實作 MarketDataPublisher 介面 — 推播掛單簿深度快照
func (h *WebSocketHandler) OnOrderBookUpdate(snapshot *engine.OrderBookSnapshot) {
	msg := map[string]any{
		"type": "depth_snapshot",
		"data": snapshot,
	}

	jsonMsg, err := json.Marshal(msg)
	if err != nil {
		log.Printf("JSON Marshal Error: %v", err)
		return
	}

	h.Broadcast(jsonMsg, "depth_snapshot")
}

// OnTrade 實作 MarketDataPublisher 介面
func (h *WebSocketHandler) OnTrade(trade *engine.Trade) {
	// 轉換為 JSON 訊息
	msg := map[string]any{
		"type": "trade",
		"data": map[string]any{
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

	h.Broadcast(jsonMsg, "trade")
}

// OnOrderUpdate 實作 MarketDataPublisher 介面
func (h *WebSocketHandler) OnOrderUpdate(order *domain.Order) {
	msg := map[string]any{
		"type": "order_update",
		"data": map[string]any{
			"id":              order.ID,
			"user_id":         order.UserID,
			"symbol":          order.Symbol,
			"side":            domain.SideToString(order.Side),
			"type":            order.Type,
			"price":           order.Price,
			"quantity":        order.Quantity,
			"filled_quantity": order.FilledQuantity,
			"status":          domain.StatusToString(order.Status),
			"created_at":      order.CreatedAt,
			"updated_at":      order.UpdatedAt,
		},
	}

	jsonMsg, err := json.Marshal(msg)
	if err != nil {
		log.Printf("JSON Marshal Error: %v", err)
		return
	}

	h.Broadcast(jsonMsg, "order_update")
}

// Run 啟動 WS 管理協程 (CSP 模型)
func (h *WebSocketHandler) Run() {
	for {
		select {
		case client := <-h.register:
			// 只有此 Goroutine 讀寫 map，不需 Lock
			h.clients[client] = true
			metrics.WebSocketConnected(h.serviceName)
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				metrics.WebSocketDisconnected(h.serviceName)
			}
		case message := <-h.broadcast:
			// 廣播不再直接呼叫網路 IO
			for client := range h.clients {
				select {
				case client.send <- message.payload:
					// 成功放入連線專屬的 send channel
				default:
					// 如果該連線的 send channel 滿了 (可能網路極慢)，剔除該連線避免拖慢整個廣播
					log.Println("WS Send Buffer Full: removing client")
					metrics.RecordWebSocketBroadcast(h.serviceName, message.messageType, "client_buffer_full")
					close(client.send)
					delete(h.clients, client)
					metrics.WebSocketDisconnected(h.serviceName)
				}
			}
		}
	}
}

// Broadcast 發送訊息給所有客戶端
// 使用 non-blocking send：若 channel 已滿則丟棄，避免阻塞呼叫方（尤其是 DB transaction 中的 OnTrade）
func (h *WebSocketHandler) Broadcast(message []byte, messageType string) {
	select {
	case h.broadcast <- outboundMessage{payload: message, messageType: messageType}:
		metrics.RecordWebSocketBroadcast(h.serviceName, messageType, "queued")
	default:
		// channel 已滿，丟棄此訊息（WebSocket 推播不影響核心撮合邏輯）
		log.Println("WS Broadcast: channel full, dropping message")
		metrics.RecordWebSocketBroadcast(h.serviceName, messageType, "dropped")
	}
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

	client := &Client{
		handler: h,
		conn:    conn,
		send:    make(chan []byte, 256), // 每個 client 有自己的寫入 buffer
	}

	h.register <- client

	// 啟動實作者負責的讀寫 Loop (pump)
	go client.writePump() // 現在由 Client 本身處理
	go client.readPump()  // 現在由 Client 本身處理
}

// readPump 處理連線讀取
func (c *Client) readPump() {
	defer func() {
		c.handler.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WS Read Error: %v", err)
			}
			break
		}
	}
}

// writePump 將訊息寫入連線 (負責網路 I/O)
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// 通道關閉時，發送 Close 訊息
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// 執行真正的網路寫入 (Blocking IO)
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
