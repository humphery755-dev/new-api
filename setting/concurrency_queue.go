package setting

import (
	"sync"
)

// 并发排队配置:同一客户端(按 user 或 token)并发超过分组上限时排队等待。
// 限额为 per-instance(进程内存量);当前单实例部署精确生效。
// 值由 custom-config.yaml 的 concurrency_queue 区块在启动时赋值(见 custom_config.go)。
const (
	ConcurrencyQueueScopeUser  = "user"
	ConcurrencyQueueScopeToken = "token"
)

var (
	ConcurrencyQueueEnabled        = false
	ConcurrencyQueueScope          = ConcurrencyQueueScopeUser
	ConcurrencyQueueDefaultLimit   = 2 // 0 = 不限
	ConcurrencyQueueTimeoutSeconds = 30
	ConcurrencyQueueGroupLimit     = map[string]int{} // 分组覆盖;值 0 = 该分组不限
	ConcurrencyQueueMutex          sync.RWMutex
)
