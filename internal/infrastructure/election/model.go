package election

// LockStatus 代表選主鎖的目前狀態
type LockStatus int

const (
	// StatusLeader 表示目前此實例是 Leader
	StatusLeader LockStatus = 1
	// StatusStandby 表示目前此實例是 Standby，等待主動競選
	StatusStandby LockStatus = 0
)

// LeaderLock 代表 partition_leader_locks 表的一行
type LeaderLock struct {
	Partition    string // Kafka Partition 識別鍵（例如 "orders:BTC-USD"）
	LeaderID     string // 目前 Leader 的實例 ID（例如 Pod Name 或 UUID）
	FencingToken int64  // 單調遞增的防腦裂令牌（每次選主成功後 +1）
	ExpiresAt    int64  // 租約到期時間（Unix 毫秒）
}
