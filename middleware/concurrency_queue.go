package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

// concurrencySlotQueue 是单个客户端 key 的计数信号量:
// slots 缓冲容量即并发上限,发送=占槽,接收=还槽;len(slots) 即当前占用数。
type concurrencySlotQueue struct {
	slots   chan struct{}
	waiting int
}

var (
	concurrencyQueues   = map[string]*concurrencySlotQueue{}
	concurrencyQueuesMu sync.Mutex
)

// ConcurrencyQueue 使同一客户端的并发请求超过分组上限时排队等待,
// 排队超过 ConcurrencyQueueTimeoutSeconds 返回 429。WebSocket 长连接不占槽。
// 限额为 per-instance(进程内存量),单实例部署精确生效。
func ConcurrencyQueue() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !setting.ConcurrencyQueueEnabled {
			c.Next()
			return
		}
		// realtime 等 WebSocket 长连接生命周期不可控,不纳入并发槽
		if strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
			c.Next()
			return
		}

		key, capacity := concurrencyQueueTarget(c)
		if capacity <= 0 {
			c.Next()
			return
		}

		timeout := time.Duration(setting.ConcurrencyQueueTimeoutSeconds) * time.Second
		acquired, clientGone := acquireConcurrencySlot(c.Request.Context(), key, capacity, timeout)
		if !acquired {
			if !clientGone {
				c.Header("Retry-After", strconv.Itoa(setting.ConcurrencyQueueTimeoutSeconds))
				abortWithOpenAiMessage(c, http.StatusTooManyRequests,
					fmt.Sprintf("当前并发请求数已达上限(%d),已排队等待 %d 秒超时,请稍后重试", capacity, setting.ConcurrencyQueueTimeoutSeconds))
			}
			c.Abort()
			return
		}
		defer releaseConcurrencySlot(key)
		c.Next()
	}
}

// concurrencyQueueTarget 返回该请求的排队 key 与有效容量(分组覆盖默认值;0=不限)。
func concurrencyQueueTarget(c *gin.Context) (string, int) {
	var key string
	if setting.ConcurrencyQueueScope == setting.ConcurrencyQueueScopeToken {
		key = "t:" + strconv.Itoa(common.GetContextKeyInt(c, constant.ContextKeyTokenId))
	} else {
		key = "u:" + strconv.Itoa(c.GetInt("id"))
	}

	group := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	if group == "" {
		group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	}
	setting.ConcurrencyQueueMutex.RLock()
	defer setting.ConcurrencyQueueMutex.RUnlock()
	if limit, ok := setting.ConcurrencyQueueGroupLimit[group]; ok {
		return key, limit
	}
	return key, setting.ConcurrencyQueueDefaultLimit
}

func acquireConcurrencySlot(ctx context.Context, key string, capacity int, timeout time.Duration) (acquired bool, clientGone bool) {
	concurrencyQueuesMu.Lock()
	q, ok := concurrencyQueues[key]
	if !ok {
		q = &concurrencySlotQueue{slots: make(chan struct{}, capacity)}
		concurrencyQueues[key] = q
	}
	q.waiting++
	concurrencyQueuesMu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case q.slots <- struct{}{}:
		concurrencyQueuesMu.Lock()
		q.waiting--
		concurrencyQueuesMu.Unlock()
		return true, false
	case <-timer.C:
		concurrencyQueuesMu.Lock()
		q.waiting--
		maybeDeleteConcurrencyQueueLocked(key, q)
		concurrencyQueuesMu.Unlock()
		return false, false
	case <-ctx.Done():
		concurrencyQueuesMu.Lock()
		q.waiting--
		maybeDeleteConcurrencyQueueLocked(key, q)
		concurrencyQueuesMu.Unlock()
		return false, true
	}
}

func releaseConcurrencySlot(key string) {
	concurrencyQueuesMu.Lock()
	defer concurrencyQueuesMu.Unlock()
	q, ok := concurrencyQueues[key]
	if !ok {
		return
	}
	<-q.slots
	maybeDeleteConcurrencyQueueLocked(key, q)
}

// maybeDeleteConcurrencyQueueLocked 在无占用且无等待者时回收空闲 key,防止 map 无限增长。
// 等待者在 select 前已计入 waiting,不会被误回收。
func maybeDeleteConcurrencyQueueLocked(key string, q *concurrencySlotQueue) {
	if q.waiting == 0 && len(q.slots) == 0 {
		delete(concurrencyQueues, key)
	}
}
