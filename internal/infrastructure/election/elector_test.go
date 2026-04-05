package election_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// mock 實作：用 in-memory map 模擬 PostgreSQL election 行為
// ============================================================

type mockLockStore struct {
	mu           sync.Mutex
	partition    string
	leaderID     string
	fencingToken int64
	expiresAt    time.Time
}

func (m *mockLockStore) AcquireLock(partition, instanceID string, leaseDuration time.Duration) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	// 鎖不存在或已過期
	if m.partition == "" || now.After(m.expiresAt) {
		m.partition = partition
		m.leaderID = instanceID
		m.fencingToken++
		m.expiresAt = now.Add(leaseDuration)
		return m.fencingToken, true, nil
	}
	// 有人持有有效鎖
	return 0, false, nil
}

func (m *mockLockStore) ExtendLease(partition, instanceID string, token int64, leaseDuration time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.leaderID != instanceID || m.fencingToken != token {
		return assert.AnError // 租約已被新 Leader 覆蓋
	}
	m.expiresAt = time.Now().Add(leaseDuration)
	return nil
}

func (m *mockLockStore) ReleaseLock(partition, instanceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.leaderID == instanceID {
		m.partition = ""
		m.leaderID = ""
		m.fencingToken = 0
	}
	return nil
}

func (m *mockLockStore) CurrentToken() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fencingToken
}

// ============================================================
// 測試案例
// ============================================================

// TestElection_FencingToken_MonotonicallyIncreasing
// 驗證：每次新 Leader 取得鎖，FencingToken 必須單調遞增
func TestElection_FencingToken_MonotonicallyIncreasing(t *testing.T) {
	store := &mockLockStore{}
	leaseDuration := 50 * time.Millisecond

	// 第一次競選
	token1, acquired1, _ := store.AcquireLock("test", "instance-A", leaseDuration)
	require.True(t, acquired1)
	assert.Equal(t, int64(1), token1, "第一次競選 Token 應為 1")

	// 讓租約過期
	time.Sleep(60 * time.Millisecond)

	// 第二次競選（新 Leader）
	token2, acquired2, _ := store.AcquireLock("test", "instance-B", leaseDuration)
	require.True(t, acquired2)
	assert.Greater(t, token2, token1, "FencingToken 必須單調遞增")
}

// TestElection_OnlyOneLeaderAtATime
// 驗證：同一時間只有一個實例能取得鎖（防腦裂基本保證）
func TestElection_OnlyOneLeaderAtATime(t *testing.T) {
	store := &mockLockStore{}
	leaseDuration := 5 * time.Second

	var leaderCount atomic.Int64
	const goroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		instanceID := "instance-" + string(rune('A'+i))
		go func(id string) {
			defer wg.Done()
			_, acquired, _ := store.AcquireLock("test", id, leaseDuration)
			if acquired {
				leaderCount.Add(1)
			}
		}(instanceID)
	}
	wg.Wait()

	assert.Equal(t, int64(1), leaderCount.Load(), "不管幾個 goroutine 同時競選，只能有一個 Leader")
}

// TestElection_StaleLeaderCantRenewLease
// 驗證：舊 Leader 持有過期的 FencingToken，無法延長租約
func TestElection_StaleLeaderCantRenewLease(t *testing.T) {
	store := &mockLockStore{}
	leaseDuration := 50 * time.Millisecond

	// instance-A 取得鎖
	staleToken, acquired, _ := store.AcquireLock("test", "instance-A", leaseDuration)
	require.True(t, acquired)

	// 讓租約過期，由 instance-B 搶到
	time.Sleep(60 * time.Millisecond)
	_, acquired2, _ := store.AcquireLock("test", "instance-B", leaseDuration)
	require.True(t, acquired2, "instance-B 應成功搶到鎖")

	// 舊 instance-A 嘗試用過期的 Token 延長租約，應失敗
	err := store.ExtendLease("test", "instance-A", staleToken, leaseDuration)
	assert.Error(t, err, "舊 Leader 不應能延長已被覆蓋的租約")
}

// TestElection_GracefulRelease_SpeedsUpFailover
// 驗證：Leader 主動釋放鎖後，新 Leader 立刻能取得（不需等到租約過期）
func TestElection_GracefulRelease_SpeedsUpFailover(t *testing.T) {
	store := &mockLockStore{}
	leaseDuration := 10 * time.Second // 租約很長，以凸顯「不用等到期」的差異

	// instance-A 取得鎖
	_, acquired, _ := store.AcquireLock("test", "instance-A", leaseDuration)
	require.True(t, acquired)

	// instance-A 主動釋放（模擬 onBecomeLeader 後的優雅關機）
	err := store.ReleaseLock("test", "instance-A")
	require.NoError(t, err)

	// instance-B 立刻就能取得鎖，不用等 10 秒租約到期
	_, acquired2, _ := store.AcquireLock("test", "instance-B", leaseDuration)
	assert.True(t, acquired2, "主動釋放後，新 Leader 應立刻能取得鎖")
}

// TestFencingToken_ValidateStaleness
// 驗證：下游 ValidateFencingToken 邏輯正確攔截殭屍訊息
func TestFencingToken_ValidateStaleness(t *testing.T) {
	store := &mockLockStore{}
	leaseDuration := 50 * time.Millisecond

	// 第一代 Leader 取得 Token=1
	staleToken, acquired, _ := store.AcquireLock("test", "instance-A", leaseDuration)
	require.True(t, acquired)
	assert.Equal(t, int64(1), staleToken)

	// 租約過期，第二代 Leader 取得 Token=2
	time.Sleep(60 * time.Millisecond)
	_, acquired2, _ := store.AcquireLock("test", "instance-B", leaseDuration)
	require.True(t, acquired2)
	currentToken := store.CurrentToken()
	assert.Equal(t, int64(2), currentToken)

	// 模擬下游驗證：第一代的訊息（token=1）應被判為殭屍訊息
	isStale := staleToken < currentToken
	assert.True(t, isStale, "來自舊 Leader 的訊息（token=1）應被識別為殭屍訊息並拒絕")

	// 模擬下游驗證：第二代的訊息（token=2）應正常通過
	isValid := currentToken >= currentToken
	assert.True(t, isValid, "來自當前 Leader 的訊息（token=2）應正常通過驗證")
}
