package engine

import "sync"

// EngineManager 管理多個交易對的撮合引擎
type EngineManager struct {
	engines map[string]*Engine
	mu      sync.RWMutex
}

// NewEngineManager 建立新的引擎管理器
func NewEngineManager() *EngineManager {
	return &EngineManager{
		engines: make(map[string]*Engine),
	}
}

// GetEngine 取得指定交易對的引擎，不存在則自動建立
func (m *EngineManager) GetEngine(symbol string) *Engine {
	m.mu.RLock()
	engine, exists := m.engines[symbol]
	m.mu.RUnlock()

	if exists {
		return engine
	}

	// 不存在則建立新引擎
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check 避免重複建立
	if engine, exists = m.engines[symbol]; exists {
		return engine
	}

	engine = NewEngine(symbol)
	m.engines[symbol] = engine
	return engine
}

// GetSymbols 返回所有已註冊的交易對
func (m *EngineManager) GetSymbols() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	symbols := make([]string, 0, len(m.engines))
	for symbol := range m.engines {
		symbols = append(symbols, symbol)
	}
	return symbols
}

// Reset 清除所有記憶體中的撮合引擎狀態。
// 在 Leader Election 發生切換時呼叫，確保新 Leader 從乾淨狀態重新載入 DB 快照，
// 防止舊 Leader 的殘留掛單資料污染新一輪的撮合結果。
func (m *EngineManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.engines = make(map[string]*Engine)
}

