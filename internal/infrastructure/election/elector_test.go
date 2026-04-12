package election

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// mockElectorRepo 是 Repository 的測試存根 (Stub)，用來模擬資料庫行為並記錄呼叫狀況
type mockElectorRepo struct {
	mu           sync.Mutex
	acquireToken int64 // 模擬 AcquireLock 成功後回傳的 Fencing Token
	acquireOK    bool  // 模擬是否成功取得 Leader 鎖
	acquireErr   error // 模擬資料庫連線錯誤
	extendErr    error // 模擬延長租約失敗（例如已被新主取代）
	releaseErr   error // 模擬釋放鎖失敗

	// 以下用於事後驗證的計數器與紀錄值
	acquireCalls   int
	extendCalls    int
	releaseCalls   int
	lastPartition  string
	lastInstanceID string
	lastToken      int64
}

// AcquireLock 模擬搶鎖邏輯
func (m *mockElectorRepo) AcquireLock(ctx context.Context, partition, instanceID string) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acquireCalls++
	m.lastPartition = partition
	m.lastInstanceID = instanceID
	return m.acquireToken, m.acquireOK, m.acquireErr
}

// ExtendLease 模擬續約邏輯
func (m *mockElectorRepo) ExtendLease(ctx context.Context, partition, instanceID string, fencingToken int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.extendCalls++
	m.lastPartition = partition
	m.lastInstanceID = instanceID
	m.lastToken = fencingToken
	return m.extendErr
}

// ReleaseLock 模擬主動釋放鎖邏輯
func (m *mockElectorRepo) ReleaseLock(ctx context.Context, partition, instanceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseCalls++
	m.lastPartition = partition
	m.lastInstanceID = instanceID
	return m.releaseErr
}

// TestElectorTick_BecomesLeaderAndInvokesCallback 驗證：從 Standby 成功轉為 Leader 的流程
func TestElectorTick_BecomesLeaderAndInvokesCallback(t *testing.T) {
	// 1. 準備背景：設定 Mock 讓他回傳成功，並拿到 Token=7
	repo := &mockElectorRepo{acquireToken: 7, acquireOK: true}
	elector := &Elector{repo: repo, partition: "orders:BTC-USD", instanceID: "instance-a"}
	callbackCount := 0

	// 2. 執行一次 Tick 邏輯
	elector.tick(context.Background(), func() {
		callbackCount++ // 成功成為 Leader 時應執行此回呼
	}, nil)

	// 3. 驗證結果
	assert.True(t, elector.IsLeader())                    // 狀態應變為 Leader
	assert.Equal(t, int64(7), elector.FencingToken())     // Token 應正確存入
	assert.Equal(t, 1, callbackCount)                     // Callback 應被呼叫 1 次
	assert.Equal(t, 1, repo.acquireCalls)                 // 應確實呼叫過資料庫搶鎖
	assert.Equal(t, "orders:BTC-USD", repo.lastPartition) // 參數應傳遞正確
	assert.Equal(t, "instance-a", repo.lastInstanceID)
}

// TestElectorTick_LosesLeadershipWhenExtendLeaseFails 驗證：Leader 在續約失敗時應自動退位（防腦裂）
func TestElectorTick_LosesLeadershipWhenExtendLeaseFails(t *testing.T) {
	// 1. 準備背景：原本是 Leader (Token=11)，但設定 Mock 續約會報錯
	repo := &mockElectorRepo{extendErr: errors.New("stale leader")}
	elector := &Elector{repo: repo, partition: "orders:BTC-USD", instanceID: "instance-a"}
	elector.isLeader.Store(true)
	elector.fencingToken.Store(11)
	lostLeadership := 0

	// 2. 執行 Tick（此時 Elector 會嘗試續約但失敗）
	elector.tick(context.Background(), nil, func() {
		lostLeadership++ // 失去身份時應執行此回呼
	})

	// 3. 驗證結果
	assert.False(t, elector.IsLeader()) // 狀態應變回 Standby
	assert.Zero(t, elector.FencingToken())
	assert.Equal(t, 1, lostLeadership)
	assert.Equal(t, 1, repo.extendCalls)       // 應呼叫過續約介面
	assert.Equal(t, int64(11), repo.lastToken) // 續約參數應包含當前 Token
}

// TestElectorTick_RemainsStandbyWhenAcquireMisses 驗證：搶鎖失敗時應安靜地保持 Standby
func TestElectorTick_RemainsStandbyWhenAcquireMisses(t *testing.T) {
	// 1. 準備背景：Mock 回傳搶鎖失敗（代表別人目前是 Leader）
	repo := &mockElectorRepo{acquireOK: false}
	elector := &Elector{repo: repo, partition: "orders:BTC-USD", instanceID: "instance-a"}
	callbackCount := 0

	// 2. 執行 Tick
	elector.tick(context.Background(), func() {
		callbackCount++
	}, nil)

	// 3. 驗證結果
	assert.False(t, elector.IsLeader())
	assert.Zero(t, elector.FencingToken())
	assert.Equal(t, 0, callbackCount) // 不應觸發成為 Leader 的回呼
	assert.Equal(t, 1, repo.acquireCalls)
}

// TestElectorRun_ReleasesLockOnContextCancel 驗證：程序關閉 (Graceful Shutdown) 時應釋放鎖
func TestElectorRun_ReleasesLockOnContextCancel(t *testing.T) {
	// 1. 準備背景：原本是 Leader
	repo := &mockElectorRepo{}
	elector := &Elector{repo: repo, partition: "orders:BTC-USD", instanceID: "instance-a", renewInterval: time.Millisecond}
	elector.isLeader.Store(true)
	elector.fencingToken.Store(13)

	// 2. 建立一個立即取消的 Context，並啟動 Run（Run 應該會偵測到 Done 並結束循環）
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	elector.Run(ctx, nil, nil)

	// 3. 驗證結果
	assert.Equal(t, 1, repo.releaseCalls) // 迴圈結束前應呼叫 ReleaseLock
	assert.False(t, elector.IsLeader())   // 狀態應清空
	assert.Zero(t, elector.FencingToken())
}
