package setting

import (
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// 并发排队配置:同一客户端(按 user 或 token)并发超过分组上限时排队等待。
// 限额为 per-instance(进程内存量);当前单实例部署精确生效。
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

func ConcurrencyQueueGroupLimit2JSONString() string {
	ConcurrencyQueueMutex.RLock()
	defer ConcurrencyQueueMutex.RUnlock()

	jsonBytes, err := common.Marshal(ConcurrencyQueueGroupLimit)
	if err != nil {
		common.SysLog("error marshalling concurrency queue group limit: " + err.Error())
		return "{}"
	}
	return string(jsonBytes)
}

func UpdateConcurrencyQueueGroupLimitByJSONString(jsonStr string) error {
	groupLimit := make(map[string]int)
	if err := common.Unmarshal([]byte(jsonStr), &groupLimit); err != nil {
		return err
	}
	for group, limit := range groupLimit {
		if limit < 0 {
			return fmt.Errorf("group %s has negative concurrency limit: %d", group, limit)
		}
	}
	ConcurrencyQueueMutex.Lock()
	defer ConcurrencyQueueMutex.Unlock()
	ConcurrencyQueueGroupLimit = groupLimit
	return nil
}
