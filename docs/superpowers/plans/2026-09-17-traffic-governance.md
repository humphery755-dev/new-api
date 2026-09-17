# 流量治理三模块实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按已确认设计实现三模块:用户并发排队中间件、客户端+模型 SWRR 加权轮询分发、渠道错误关键字规则引擎。

**Architecture:** 模块 1 用进程内 per-key 信号量(`chan struct{}` 槽位池)在 relay 链路前置排队;模块 2 在 `GetRandomSatisfiedChannel` 同优先级层新增 SWRR 分支(开关默认关,零行为变化);模块 3 以「关键字→动作」规则表在 `ShouldDisableChannel`/`ShouldRetryRelayError` 开头短路,未命中回落现有逻辑。全部配置走既有 options 表 + `SyncOptions` 60s 热更新,无 schema 变更。

**Tech Stack:** Go 1.25 / Gin / GORM(仅既有 options 机制)/ testify / React 19 + Rsbuild(bun)

**Spec:** `docs/superpowers/specs/2026-09-17-traffic-governance-design.md`

## Global Constraints

- JSON 序列化只用 `common.Marshal` / `common.Unmarshal`(`common/json.go`),禁止直接 `encoding/json`(类型引用 `json.RawMessage` 等不受限)。
- 新 Go 代码遵循 AGENTS.md 现代约定:`any` 替代 `interface{}`、`slices`/`maps` 工具、`for i := range n`。
- 测试用 `github.com/stretchr/testify/require`(fatal)与 `assert`(非 fatal),fixture 内显式初始化全局状态并用 `t.Cleanup` 还原。
- 修改过的 Go 文件必须 `gofmt` 且无未用 import。
- `relaykit/` 不涉及;本计划不触碰三库矩阵(无 schema 变更)。
- 前端用 bun;用户可见文案必须 i18n:`useTranslation()` + `t('English key')`,key 为英文源串;中文翻译追加到 `web/src/i18n/locales/zh.json`。
- 新增代码一律新文件或单点插入,遵守 fork「Add Over Modify」治理。
- 每个 Task 结束:`gofmt -l <changed>.go` 输出为空 + 相关包 `go build ./...` 通过 + 该任务测试 PASS。

---

## 模块 1:用户并发排队中间件

### Task 1: 配置层与热更新注册

**Files:**
- Create: `setting/concurrency_queue.go`
- Modify: `model/option.go`(三处插入,锚点见步骤)
- Test: `setting/concurrency_queue_test.go`

**Interfaces:**
- Consumes: 无(首任务)。
- Produces(后续任务依赖,签名逐字):
  - `setting.ConcurrencyQueueEnabled bool`
  - `setting.ConcurrencyQueueScope string`(`"user"` | `"token"`)
  - `setting.ConcurrencyQueueDefaultLimit int`
  - `setting.ConcurrencyQueueTimeoutSeconds int`
  - `setting.ConcurrencyQueueGroupLimit map[string]int`
  - `setting.ConcurrencyQueueMutex sync.RWMutex`
  - `setting.ConcurrencyQueueScopeUser = "user"`、`setting.ConcurrencyQueueScopeToken = "token"`(常量)
  - `setting.UpdateConcurrencyQueueGroupLimitByJSONString(jsonStr string) error`

- [ ] **Step 1: 写配置解析的失败测试**

创建 `setting/concurrency_queue_test.go`:

```go
package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateConcurrencyQueueGroupLimitValid(t *testing.T) {
	original := ConcurrencyQueueGroupLimit
	t.Cleanup(func() { ConcurrencyQueueGroupLimit = original })

	err := UpdateConcurrencyQueueGroupLimitByJSONString(`{"default":2,"vip":5}`)
	require.NoError(t, err)
	assert.Equal(t, 2, ConcurrencyQueueGroupLimit["default"])
	assert.Equal(t, 5, ConcurrencyQueueGroupLimit["vip"])
}

func TestUpdateConcurrencyQueueGroupLimitRejectsNegative(t *testing.T) {
	err := UpdateConcurrencyQueueGroupLimitByJSONString(`{"bad":-1}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "negative")
}

func TestUpdateConcurrencyQueueGroupLimitRejectsInvalidJSON(t *testing.T) {
	err := UpdateConcurrencyQueueGroupLimitByJSONString(`not-json`)
	require.Error(t, err)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./setting/ -run TestUpdateConcurrencyQueueGroupLimit -v`
Expected: FAIL,`undefined: UpdateConcurrencyQueueGroupLimitByJSONString`

- [ ] **Step 3: 实现 `setting/concurrency_queue.go`**

```go
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
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./setting/ -run TestUpdateConcurrencyQueueGroupLimit -v`
Expected: PASS(3 个测试)

- [ ] **Step 5: option.go 三处插入**

5a. 默认值注册 —— `model/option.go` 找到锚点行:

```go
common.OptionMap["ModelRequestRateLimitGroup"] = setting.ModelRequestRateLimitGroup2JSONString()
```

紧随其后插入:

```go
common.OptionMap["ConcurrencyQueueEnabled"] = strconv.FormatBool(setting.ConcurrencyQueueEnabled)
common.OptionMap["ConcurrencyQueueScope"] = setting.ConcurrencyQueueScope
common.OptionMap["ConcurrencyQueueDefaultLimit"] = strconv.Itoa(setting.ConcurrencyQueueDefaultLimit)
common.OptionMap["ConcurrencyQueueTimeoutSeconds"] = strconv.Itoa(setting.ConcurrencyQueueTimeoutSeconds)
common.OptionMap["ConcurrencyQueueGroupLimit"] = setting.ConcurrencyQueueGroupLimit2JSONString()
```

5b. bool 分支 —— 在 `updateOptionMap` 中找到锚点:

```go
		case "ModelRequestRateLimitEnabled":
			setting.ModelRequestRateLimitEnabled = boolValue
```

紧随其后插入:

```go
		case "ConcurrencyQueueEnabled":
			setting.ConcurrencyQueueEnabled = boolValue
```

5c. 字符串/整数分支 —— 找到锚点:

```go
	case "ModelRequestRateLimitGroup":
		err = setting.UpdateModelRequestRateLimitGroupByJSONString(value)
```

紧随其后插入:

```go
	case "ConcurrencyQueueScope":
		setting.ConcurrencyQueueScope = value
	case "ConcurrencyQueueDefaultLimit":
		setting.ConcurrencyQueueDefaultLimit, _ = strconv.Atoi(value)
	case "ConcurrencyQueueTimeoutSeconds":
		setting.ConcurrencyQueueTimeoutSeconds, _ = strconv.Atoi(value)
	case "ConcurrencyQueueGroupLimit":
		err = setting.UpdateConcurrencyQueueGroupLimitByJSONString(value)
```

- [ ] **Step 6: 构建与提交**

Run: `gofmt -l setting/concurrency_queue.go setting/concurrency_queue_test.go model/option.go`(输出须为空)
Run: `go build ./... && go test ./setting/ -run TestUpdateConcurrencyQueueGroupLimit -v`

```bash
git add setting/concurrency_queue.go setting/concurrency_queue_test.go model/option.go
git commit -m "feat(setting): concurrency queue configuration with options hot-reload"
```

### Task 2: 并发排队中间件核心

**Files:**
- Create: `middleware/concurrency_queue.go`
- Test: `middleware/concurrency_queue_test.go`

**Interfaces:**
- Consumes: Task 1 全部 `setting.ConcurrencyQueue*`;`middleware.abortWithOpenAiMessage`(`middleware/utils.go:14`,签名 `func(c *gin.Context, statusCode int, message string, code ...types.ErrorCode)`);`common.GetContextKeyInt/String`(`common/gin.go:165/169`);`constant.ContextKeyTokenId/ContextKeyTokenGroup/ContextKeyUserGroup`。
- Produces: `middleware.ConcurrencyQueue() gin.HandlerFunc`(Task 3 挂载用)。

- [ ] **Step 1: 写失败的中间件测试**

创建 `middleware/concurrency_queue_test.go`:

```go
package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupConcurrencyQueueFixture(t *testing.T, enabled bool, scope string, defLimit, timeoutSeconds int, groupLimit map[string]int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	origEnabled, origScope, origDef, origTimeout, origGroup := setting.ConcurrencyQueueEnabled,
		setting.ConcurrencyQueueScope, setting.ConcurrencyQueueDefaultLimit,
		setting.ConcurrencyQueueTimeoutSeconds, setting.ConcurrencyQueueGroupLimit
	setting.ConcurrencyQueueEnabled = enabled
	setting.ConcurrencyQueueScope = scope
	setting.ConcurrencyQueueDefaultLimit = defLimit
	setting.ConcurrencyQueueTimeoutSeconds = timeoutSeconds
	setting.ConcurrencyQueueGroupLimit = groupLimit
	t.Cleanup(func() {
		setting.ConcurrencyQueueEnabled = origEnabled
		setting.ConcurrencyQueueScope = origScope
		setting.ConcurrencyQueueDefaultLimit = origDef
		setting.ConcurrencyQueueTimeoutSeconds = origTimeout
		setting.ConcurrencyQueueGroupLimit = origGroup
		concurrencyQueuesMu.Lock()
		concurrencyQueues = map[string]*concurrencySlotQueue{}
		concurrencyQueuesMu.Unlock()
	})
}

func newConcurrencyQueueTestContext(t *testing.T, userId int, tokenId int, tokenGroup string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request = req
	c.Set("id", userId)
	common.SetContextKey(c, constant.ContextKeyTokenId, tokenId)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, tokenGroup)
	return c, w
}

func TestConcurrencyQueueDisabledPassesThrough(t *testing.T) {
	setupConcurrencyQueueFixture(t, false, "user", 1, 30, nil)
	c, w := newConcurrencyQueueTestContext(t, 1, 0, "default")
	ConcurrencyQueue()(c)
	c.Next()
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, len(concurrencyQueues))
}

func TestConcurrencyQueueThirdRequestQueuesUntilRelease(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "user", 2, 30, nil)

	block := make(chan struct{})
	handler := func(c *gin.Context) {
		<-block
		c.Status(http.StatusOK)
	}

	c1, _ := newConcurrencyQueueTestContext(t, 1, 0, "default")
	c2, _ := newConcurrencyQueueTestContext(t, 1, 0, "default")
	c3, _ := newConcurrencyQueueTestContext(t, 1, 0, "default")

	done1 := make(chan struct{})
	go func() {
		ConcurrencyQueue()(c1)
		handler(c1)
		c1.Next()
		close(done1)
	}()
	waitForSlots(t, "u:1", 1)

	done2 := make(chan struct{})
	go func() {
		ConcurrencyQueue()(c2)
		handler(c2)
		c2.Next()
		close(done2)
	}()
	waitForSlots(t, "u:1", 2)

	done3 := make(chan struct{})
	go func() {
		ConcurrencyQueue()(c3)
		handler(c3)
		c3.Next()
		close(done3)
	}()
	waitForWaiters(t, "u:1", 1)
	select {
	case <-done3:
		t.Fatal("third request must be queued, not completed")
	default:
	}

	close(block)
	<-done1
	<-done2
	<-done3
	waitForCondition(t, func() bool { return len(concurrencyQueues) == 0 })
}

func TestConcurrencyQueueTimeoutReturns429(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "user", 1, 1, nil)

	c1, _ := newConcurrencyQueueTestContext(t, 1, 0, "default")
	done1 := make(chan struct{})
	go func() {
		ConcurrencyQueue()(c1)
		c1.Next()
		close(done1)
	}()
	waitForSlots(t, "u:1", 1)

	c2, w2 := newConcurrencyQueueTestContext(t, 1, 0, "default")
	ConcurrencyQueue()(c2)
	c2.Next()

	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
	assert.Equal(t, "1", w2.Header().Get("Retry-After"))
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("first request never finished")
	}
	waitForCondition(t, func() bool { return len(concurrencyQueues) == 0 })
}

func TestConcurrencyQueueWebSocketUpgradePassesThrough(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "user", 1, 30, nil)
	c, _ := newConcurrencyQueueTestContext(t, 1, 0, "default")
	c.Request.Header.Set("Upgrade", "websocket")
	ConcurrencyQueue()(c)
	c.Next()
	assert.Equal(t, http.StatusOK, c.Writer.Status())
	assert.Equal(t, 0, len(concurrencyQueues))
}

func TestConcurrencyQueueZeroCapacityPassesThrough(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "user", 0, 30, map[string]int{"default": 0})
	c, _ := newConcurrencyQueueTestContext(t, 1, 0, "default")
	ConcurrencyQueue()(c)
	c.Next()
	assert.Equal(t, http.StatusOK, c.Writer.Status())
	assert.Equal(t, 0, len(concurrencyQueues))
}

func TestConcurrencyQueueGroupLimitOverridesDefault(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "user", 3, 1, map[string]int{"vip": 1})

	c1, _ := newConcurrencyQueueTestContext(t, 1, 0, "vip")
	done1 := make(chan struct{})
	go func() {
		ConcurrencyQueue()(c1)
		c1.Next()
		close(done1)
	}()
	waitForSlots(t, "u:1", 1)

	c2, w2 := newConcurrencyQueueTestContext(t, 1, 0, "vip")
	ConcurrencyQueue()(c2)
	c2.Next()
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
}

func TestConcurrencyQueueTokenScopeIsolatesKeys(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "token", 1, 30, nil)

	c1, _ := newConcurrencyQueueTestContext(t, 1, 100, "default")
	c2, w2 := newConcurrencyQueueTestContext(t, 1, 200, "default")
	done1 := make(chan struct{})
	go func() {
		ConcurrencyQueue()(c1)
		c1.Next()
		close(done1)
	}()
	waitForSlots(t, "t:100", 1)

	ConcurrencyQueue()(c2)
	c2.Next()
	assert.Equal(t, http.StatusOK, w2.Code, "different token => different queue key")
	waitForCondition(t, func() bool {
		concurrencyQueuesMu.Lock()
		defer concurrencyQueuesMu.Unlock()
		_, ok := concurrencyQueues["t:100"]
		return !ok
	})
}

func TestConcurrencyQueueClientDisconnectWhileQueued(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "user", 1, 30, nil)

	c1, _ := newConcurrencyQueueTestContext(t, 1, 0, "default")
	done1 := make(chan struct{})
	go func() {
		ConcurrencyQueue()(c1)
		c1.Next()
		close(done1)
	}()
	waitForSlots(t, "u:1", 1)

	// 排队中的请求,客户端断开(context 取消)后必须让出且不写 429
	c2, w2 := newConcurrencyQueueTestContext(t, 1, 0, "default")
	ctx, cancel := context.WithCancel(c2.Request.Context())
	c2.Request = c2.Request.WithContext(ctx)
	done2 := make(chan struct{})
	go func() {
		ConcurrencyQueue()(c2)
		c2.Next()
		close(done2)
	}()
	waitForWaiters(t, "u:1", 1)

	cancel()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnected request never returned from queue")
	}
	assert.Equal(t, http.StatusOK, w2.Code, "no 429 body written after client disconnect")
	assert.True(t, done2ClosedWithoutAbort(w2))

	close(done1)
	waitForCondition(t, func() bool { return len(concurrencyQueues) == 0 })
}

func done2ClosedWithoutAbort(w *httptest.ResponseRecorder) bool {
	// 客户端断开路径静默返回:未调用 abortWithOpenAiMessage,响应体为空
	return w.Body.Len() == 0
}

// --- helpers ---

func waitForSlots(t *testing.T, key string, want int) {
	t.Helper()
	require.Eventually(t, func() bool {
		concurrencyQueuesMu.Lock()
		defer concurrencyQueuesMu.Unlock()
		q, ok := concurrencyQueues[key]
		return ok && len(q.slots) == want
	}, 2*time.Second, 5*time.Millisecond)
}

func waitForWaiters(t *testing.T, key string, want int) {
	t.Helper()
	require.Eventually(t, func() bool {
		concurrencyQueuesMu.Lock()
		defer concurrencyQueuesMu.Unlock()
		q, ok := concurrencyQueues[key]
		return ok && q.waiting == want
	}, 2*time.Second, 5*time.Millisecond)
}

func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 2*time.Second, 5*time.Millisecond)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./middleware/ -run TestConcurrencyQueue -v`
Expected: FAIL,`undefined: ConcurrencyQueue`

- [ ] **Step 3: 实现 `middleware/concurrency_queue.go`**

```go
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
```

- [ ] **Step 4: 运行确认通过**

Run: `gofmt -w middleware/concurrency_queue.go middleware/concurrency_queue_test.go && go vet ./middleware/ && go test ./middleware/ -run TestConcurrencyQueue -v`
Expected: PASS(8 个测试)

- [ ] **Step 5: 提交**

```bash
git add middleware/concurrency_queue.go middleware/concurrency_queue_test.go
git commit -m "feat(middleware): per-client concurrency queue with timeout and websocket bypass"
```

### Task 3: 路由挂载

**Files:**
- Modify: `router/relay-router.go`(80 行、203 行两处)
- Modify: `router/video-router.go`(18 行)
- Modify: `router/task-plugin-protocol-router.go`(32 行、40 行)
- Modify: `router/plugin-router.go`(121 行)

**Interfaces:**
- Consumes: `middleware.ConcurrencyQueue()`(Task 2)。
- Produces: relay 链路全部挂载排队中间件。

- [ ] **Step 1: 六处挂载,均在现有 `middleware.ModelRequestRateLimit()` 之后并列添加 `middleware.ConcurrencyQueue()`

以锚文本定位逐处修改:

1. `router/relay-router.go` 锚点 `relayV1Router.Use(middleware.ModelRequestRateLimit())` 之后加一行:
   `relayV1Router.Use(middleware.ConcurrencyQueue())`
2. 同文件锚点 `relayGeminiRouter.Use(middleware.ModelRequestRateLimit())` 之后加一行:
   `relayGeminiRouter.Use(middleware.ConcurrencyQueue())`
3. `router/video-router.go` 锚点 `middleware.TaskPluginEndpointOnly(middleware.ModelRequestRateLimit()),` 之后加一行:
   `middleware.ConcurrencyQueue(),`
4. `router/task-plugin-protocol-router.go` 锚点 `middleware.ModelRequestRateLimit(), middleware.PinTaskPluginEndpoint(),`(32 行处)在 `middleware.ModelRequestRateLimit(),` 之后插入:
   `middleware.ConcurrencyQueue(),`
5. 同文件 40 行处锚点 `middleware.TaskPluginEndpointOnly(middleware.ModelRequestRateLimit()),` 之后插入:
   `middleware.ConcurrencyQueue(),`
6. `router/plugin-router.go` 锚点 `middleware.ModelRequestRateLimit(),` 之后插入:
   `middleware.ConcurrencyQueue(),`

- [ ] **Step 2: 构建验证 + 提交**

Run: `gofmt -l router/`(输出须为空)&& `go build ./...`
Expected: 构建成功

```bash
git add router/
git commit -m "feat(router): mount concurrency queue middleware on all relay routes"
```

### Task 4: 前端「并发排队」配置 UI

**Files:**
- Create: `web/src/features/system-settings/request-limits/concurrency-queue-section.tsx`
- Modify: `web/src/features/system-settings/security/section-registry.tsx`(注册新 section,仿 `rate-limit` 项)
- Modify: `web/src/features/system-settings/types.ts`(Settings 类型加字段,与 `ModelRequestRateLimit*` 同接口)
- Modify: `web/src/i18n/locales/zh.json`(追加中文翻译)

**Interfaces:**
- Consumes: 后端 options `ConcurrencyQueueEnabled/Scope/DefaultLimit/TimeoutSeconds/GroupLimit`;现有 `useUpdateOption`、`SettingsSection`、`SettingsForm`、`JsonCodeEditor` 组件(用法以同目录 `rate-limit-section.tsx` 为准)。
- Produces: 管理员配置 UI。

- [ ] **Step 1: types.ts 加字段**

在 `types.ts` 中 `ModelRequestRateLimitDurationMinutes` 字段所在接口内追加:

```ts
ConcurrencyQueueEnabled: boolean
ConcurrencyQueueScope: string
ConcurrencyQueueDefaultLimit: number
ConcurrencyQueueTimeoutSeconds: number
ConcurrencyQueueGroupLimit: string
```

- [ ] **Step 2: 新建组件**

创建 `concurrency-queue-section.tsx`(仿 `rate-limit-section.tsx` 的 zod + `SettingsForm` + `useUpdateOption` 模式;AGENTS.md 要求先读 `web/AGENTS.md`):

```tsx
/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import { JsonCodeEditor } from '@/components/json-code-editor'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const isValidGroupLimitJSON = (value: string | undefined) => {
  if (!value || value.trim() === '') return true
  try {
    const parsed = JSON.parse(value)
    if (typeof parsed !== 'object' || Array.isArray(parsed)) return false
    for (const [, val] of Object.entries(parsed)) {
      if (typeof val !== 'number' || val < 0) return false
    }
    return true
  } catch {
    return false
  }
}

const createSchema = (t: (key: string) => string) =>
  z.object({
    ConcurrencyQueueEnabled: z.boolean(),
    ConcurrencyQueueScope: z.enum(['user', 'token']),
    ConcurrencyQueueDefaultLimit: z.number().min(0),
    ConcurrencyQueueTimeoutSeconds: z.number().min(1),
    ConcurrencyQueueGroupLimit: z
      .string()
      .optional()
      .refine(isValidGroupLimitJSON, {
        message: t('Invalid JSON format or values out of allowed range'),
      }),
  })

type FormValues = z.infer<ReturnType<typeof createSchema>>

export function ConcurrencyQueueSection({
  settings,
  refetch,
}: {
  settings: Partial<FormValues> & Record<string, any>
  refetch: () => void
}) {
  const { t } = useTranslation()
  const form = useForm<FormValues>({
    resolver: zodResolver(createSchema(t)),
    defaultValues: {
      ConcurrencyQueueEnabled: false,
      ConcurrencyQueueScope: 'user',
      ConcurrencyQueueDefaultLimit: 2,
      ConcurrencyQueueTimeoutSeconds: 30,
      ConcurrencyQueueGroupLimit: '',
    },
  })
  const updateOption = useUpdateOption()

  useEffect(() => {
    form.reset({
      ConcurrencyQueueEnabled: Boolean(settings.ConcurrencyQueueEnabled),
      ConcurrencyQueueScope:
        settings.ConcurrencyQueueScope === 'token' ? 'token' : 'user',
      ConcurrencyQueueDefaultLimit: Number(
        settings.ConcurrencyQueueDefaultLimit ?? 2,
      ),
      ConcurrencyQueueTimeoutSeconds: Number(
        settings.ConcurrencyQueueTimeoutSeconds ?? 30,
      ),
      ConcurrencyQueueGroupLimit: String(
        settings.ConcurrencyQueueGroupLimit ?? '',
      ),
    })
  }, [settings, form])

  const onSubmit = (values: FormValues) => {
    updateOption(
      'ConcurrencyQueueEnabled',
      String(values.ConcurrencyQueueEnabled),
    )
    updateOption('ConcurrencyQueueScope', values.ConcurrencyQueueScope)
    updateOption(
      'ConcurrencyQueueDefaultLimit',
      String(values.ConcurrencyQueueDefaultLimit),
    )
    updateOption(
      'ConcurrencyQueueTimeoutSeconds',
      String(values.ConcurrencyQueueTimeoutSeconds),
    )
    updateOption(
      'ConcurrencyQueueGroupLimit',
      values.ConcurrencyQueueGroupLimit ?? '',
    )
    refetch()
  }

  return (
    <SettingsSection
      title={t('Concurrency Queue')}
      description={t(
        'Queue requests per client when concurrency exceeds the limit instead of rejecting them',
      )}
    >
      <SettingsForm form={form} onSubmit={onSubmit}>
        <FormField
          control={form.control}
          name='ConcurrencyQueueEnabled'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Enable Concurrency Queue')}</FormLabel>
              <FormControl>
                <Switch
                  checked={field.value}
                  onCheckedChange={(checked) => field.onChange(checked)}
                />
              </FormControl>
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='ConcurrencyQueueScope'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Client Dimension')}</FormLabel>
              <Select onValueChange={field.onChange} value={field.value}>
                <FormControl>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                </FormControl>
                <SelectContent>
                  <SelectItem value='user'>{t('Per User')}</SelectItem>
                  <SelectItem value='token'>{t('Per Token')}</SelectItem>
                </SelectContent>
              </Select>
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='ConcurrencyQueueDefaultLimit'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Default Concurrency Limit')}</FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min={0}
                  {...field}
                  onChange={(e) => field.onChange(Number(e.target.value) || 0)}
                />
              </FormControl>
              <FormDescription>
                {t('0 means unlimited. Group limits override this value.')}
              </FormDescription>
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='ConcurrencyQueueTimeoutSeconds'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Queue Timeout (seconds)')}</FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min={1}
                  {...field}
                  onChange={(e) => field.onChange(Number(e.target.value) || 1)}
                />
              </FormControl>
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='ConcurrencyQueueGroupLimit'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Group Concurrency Limits')}</FormLabel>
              <FormControl>
                <JsonCodeEditor
                  value={field.value ?? ''}
                  onChange={field.onChange}
                  placeholder='{"default": 2, "vip": 5}'
                />
              </FormControl>
              <FormDescription>
                {t('JSON object, group name to limit')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <SettingsPageFormActions />
      </SettingsForm>
    </SettingsSection>
  )
}
```

执行者注意:`SettingsForm`/`SettingsSection`/`SettingsPageFormActions`/`JsonCodeEditor`/`useUpdateOption` 的确切 props 以 `rate-limit-section.tsx` 现有用法为准(同一目录已 import 这些组件);若 props 名不同,按现有用法对齐,不新建组件。

- [ ] **Step 3: 注册 section**

在 `web/src/features/system-settings/security/section-registry.tsx` 中仿照 `rate-limit` 项(import `RateLimitSection` 并传 settings 映射)添加:

```tsx
{
  id: 'concurrency-queue',
  /* title/description 与相邻项同结构 */
  element: (
    <ConcurrencyQueueSection
      settings={{
        ConcurrencyQueueEnabled: settings.ConcurrencyQueueEnabled,
        ConcurrencyQueueScope: settings.ConcurrencyQueueScope,
        ConcurrencyQueueDefaultLimit: settings.ConcurrencyQueueDefaultLimit,
        ConcurrencyQueueTimeoutSeconds: settings.ConcurrencyQueueTimeoutSeconds,
        ConcurrencyQueueGroupLimit: settings.ConcurrencyQueueGroupLimit,
      }}
      refetch={refetch}
    />
  ),
}
```

`element`/`title` 等键名与相邻 `rate-limit` 项的实际结构保持一致;`settings` 对象与 `refetch` 来自该文件中现有项的同一来源。

- [ ] **Step 4: i18n 中文翻译**

`web/src/i18n/locales/zh.json` 追加(key 为英文源串):

```json
"Concurrency Queue": "并发排队",
"Queue requests per client when concurrency exceeds the limit instead of rejecting them": "同一客户端并发超限时排队等待而非拒绝",
"Enable Concurrency Queue": "启用并发排队",
"Client Dimension": "客户端维度",
"Per User": "按用户",
"Per Token": "按令牌",
"Default Concurrency Limit": "默认并发上限",
"0 means unlimited. Group limits override this value.": "0 表示不限;分组配置覆盖此值",
"Queue Timeout (seconds)": "排队超时(秒)",
"Group Concurrency Limits": "分组并发上限",
"JSON object, group name to limit": "JSON 对象,分组名到并发上限"
```

- [ ] **Step 5: 构建验证 + 提交**

Run: `cd web && bun run build`
Expected: 构建成功

```bash
git add web/src/features/system-settings/ web/src/i18n/locales/zh.json
git commit -m "feat(web): concurrency queue settings section"
```

---

## 模块 2:客户端+模型 SWRR 加权轮询分发

### Task 5: 轮询配置 + SWRR 状态核心

**Files:**
- Create: `setting/channel_polling.go`
- Create: `model/channel_cache_swrr.go`
- Modify: `model/option.go`(三处插入)
- Test: `model/channel_cache_swrr_test.go`

**Interfaces:**
- Consumes: `Channel.GetWeight() int`(`model/channel.go:516`,`Weight *uint` nil→0)、`Channel.Id int`。
- Produces(后续任务依赖,签名逐字):
  - `setting.ChannelPollingEnabled bool`
  - `setting.ChannelPollingScope string`(`"user"` | `"token"`)
  - `model.selectChannelByPolling(clientKey, modelName string, channels []*Channel) *Channel`(Task 6 同包调用;返回 nil 表示未启用/空集,调用方回落随机路径)

- [ ] **Step 1: 写失败的 SWRR 测试**

创建 `model/channel_cache_swrr_test.go`:

```go
package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pollingTestChannel(id int, weight uint) *Channel {
	return &Channel{Id: id, Weight: &weight}
}

func setupPollingFixture(t *testing.T, enabled bool, scope string) {
	t.Helper()
	origEnabled, origScope := setting.ChannelPollingEnabled, setting.ChannelPollingScope
	setting.ChannelPollingEnabled = enabled
	setting.ChannelPollingScope = scope
	t.Cleanup(func() {
		setting.ChannelPollingEnabled = origEnabled
		setting.ChannelPollingScope = origScope
		pollingStatesMu.Lock()
		pollingStates = map[string]*swrrState{}
		pollingStatesMu.Unlock()
	})
}

func TestSelectChannelByPollingWeightTwoToOne(t *testing.T) {
	setupPollingFixture(t, true, "user")
	channels := []*Channel{pollingTestChannel(11, 2), pollingTestChannel(22, 1)}

	var got []int
	for range 6 {
		ch := selectChannelByPolling("u:1", "L2-ALL", channels)
		require.NotNil(t, ch)
		got = append(got, ch.Id)
	}
	assert.Equal(t, []int{11, 22, 11, 11, 22, 11}, got, "SWRR 2:1 deterministic sequence")
}

func TestSelectChannelByPollingPerClientIndependence(t *testing.T) {
	setupPollingFixture(t, true, "user")
	channels := []*Channel{pollingTestChannel(11, 2), pollingTestChannel(22, 1)}

	// 客户端 A 消费两轮后,客户端 B 的序列仍从头开始
	for range 2 {
		selectChannelByPolling("u:1", "L2-ALL", channels)
	}
	first := selectChannelByPolling("u:2", "L2-ALL", channels)
	require.NotNil(t, first)
	assert.Equal(t, 11, first.Id)
}

func TestSelectChannelByPollingRebuildsOnCandidateChange(t *testing.T) {
	setupPollingFixture(t, true, "user")
	channels := []*Channel{pollingTestChannel(11, 2), pollingTestChannel(22, 1)}
	selectChannelByPolling("u:1", "L2-ALL", channels)

	// 渠道 22 禁用移除后,候选集变化 → state 重建,11 独占
	newChannels := []*Channel{pollingTestChannel(11, 2)}
	got := selectChannelByPolling("u:1", "L2-ALL", newChannels)
	require.NotNil(t, got)
	assert.Equal(t, 11, got.Id)

	pollingStatesMu.Lock()
	defer pollingStatesMu.Unlock()
	st, ok := pollingStates["u:1|L2-ALL"]
	require.True(t, ok)
	assert.Equal(t, "11:2,", st.signature)
}

func TestSelectChannelByPollingZeroWeightRotatesEvenly(t *testing.T) {
	setupPollingFixture(t, true, "user")
	channels := []*Channel{pollingTestChannel(11, 0), pollingTestChannel(22, 0)}

	var got []int
	for range 4 {
		ch := selectChannelByPolling("u:1", "L2-ALL", channels)
		require.NotNil(t, ch)
		got = append(got, ch.Id)
	}
	assert.Equal(t, []int{11, 22, 11, 22}, got)
}

func TestSelectChannelByPollingDisabledReturnsNil(t *testing.T) {
	setupPollingFixture(t, false, "user")
	channels := []*Channel{pollingTestChannel(11, 2)}
	assert.Nil(t, selectChannelByPolling("u:1", "L2-ALL", channels))
	assert.Nil(t, selectChannelByPolling("", "L2-ALL", channels), "empty clientKey never polls")
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./model/ -run TestSelectChannelByPolling -v`
Expected: FAIL,`undefined: selectChannelByPolling`

- [ ] **Step 3: 实现 `model/channel_cache_swrr.go`**

```go
package model

import (
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/setting"
)

// swrrState 保存单个 (clientKey|model) 的平滑加权轮询游标。
type swrrState struct {
	signature string
	weights   map[int]int // channelId -> currentWeight
}

var (
	pollingStates   = map[string]*swrrState{}
	pollingStatesMu sync.Mutex
)

// maxPollingStates 是轮询状态的容量上限;超限时整体清零。
// 轮询游标只影响分发顺序,不影响正确性,清零可接受。
const maxPollingStates = 10000

// pollingCandidateSignature 是候选集签名;渠道集合或权重变化后签名失配即重建 state。
func pollingCandidateSignature(channels []*Channel) string {
	var b strings.Builder
	for _, ch := range channels {
		b.WriteString(strconv.Itoa(ch.Id))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(ch.GetWeight()))
		b.WriteByte(',')
	}
	return b.String()
}

// selectChannelByPolling 在同优先级候选渠道内做平滑加权轮询(SWRR)。
// clientKey 为空或开关关闭时返回 nil,调用方回落加权随机路径。
// 零/负权重渠道以权重 1 参与均等轮换(与随机路径的全零平滑语义一致)。
// 轮询状态为 per-instance(进程内存量),单实例部署精确生效。
func selectChannelByPolling(clientKey, modelName string, channels []*Channel) *Channel {
	if len(channels) == 0 || clientKey == "" || !setting.ChannelPollingEnabled {
		return nil
	}

	signature := pollingCandidateSignature(channels)
	pollingStatesMu.Lock()
	defer pollingStatesMu.Unlock()

	key := clientKey + "|" + modelName
	st, ok := pollingStates[key]
	if !ok || st.signature != signature {
		st = &swrrState{signature: signature, weights: make(map[int]int, len(channels))}
		if len(pollingStates) >= maxPollingStates {
			pollingStates = map[string]*swrrState{}
		}
		pollingStates[key] = st
	}

	total := 0
	for _, ch := range channels {
		w := ch.GetWeight()
		if w <= 0 {
			w = 1
		}
		total += w
		st.weights[ch.Id] += w
	}

	var selected *Channel
	for _, ch := range channels {
		if selected == nil || st.weights[ch.Id] > st.weights[selected.Id] {
			selected = ch
		}
	}
	if selected == nil {
		return nil
	}
	st.weights[selected.Id] -= total
	return selected
}
```

- [ ] **Step 4: 运行确认通过**

Run: `gofmt -w model/channel_cache_swrr.go model/channel_cache_swrr_test.go && go test ./model/ -run TestSelectChannelByPolling -v`
Expected: PASS(5 个测试)

- [ ] **Step 5: 新增轮询配置与 option 注册**

创建 `setting/channel_polling.go`:

```go
package setting

// 渠道加权轮询配置:开启后同一客户端对同一模型在同优先级候选渠道内轮流分发。
// 状态为 per-instance;关闭时保持上游加权随机行为。
const (
	ChannelPollingScopeUser  = "user"
	ChannelPollingScopeToken = "token"
)

var (
	ChannelPollingEnabled = false
	ChannelPollingScope   = ChannelPollingScopeUser
)
```

`model/option.go` 三处插入:

5a. 默认值注册 —— 锚点 `common.OptionMap["ConcurrencyQueueGroupLimit"] = setting.ConcurrencyQueueGroupLimit2JSONString()`(Task 1 插入块末行)之后:

```go
common.OptionMap["ChannelPollingEnabled"] = strconv.FormatBool(setting.ChannelPollingEnabled)
common.OptionMap["ChannelPollingScope"] = setting.ChannelPollingScope
```

5b. bool 分支 —— 锚点 `case "ConcurrencyQueueEnabled":`(Task 1 插入块)之后:

```go
		case "ChannelPollingEnabled":
			setting.ChannelPollingEnabled = boolValue
```

5c. 字符串分支 —— 锚点 `case "ConcurrencyQueueGroupLimit":`(Task 1 插入块)之后:

```go
	case "ChannelPollingScope":
		setting.ChannelPollingScope = value
```

- [ ] **Step 6: 全量验证 + 提交**

Run: `gofmt -l setting/channel_polling.go model/channel_cache_swrr.go model/channel_cache_swrr_test.go model/option.go`(输出须为空)
Run: `go build ./... && go test ./model/ -run TestSelectChannelByPolling -v`

```bash
git add setting/channel_polling.go model/channel_cache_swrr.go model/channel_cache_swrr_test.go model/option.go
git commit -m "feat(model): SWRR weighted polling state for channel selection"
```

### Task 6: 渠道选择集成

**Files:**
- Modify: `model/channel_cache.go`(`GetRandomSatisfiedChannel` 重构为包装 + `GetRandomSatisfiedChannelWithClient` 变体)
- Modify: `service/channel_select.go`(`CacheGetRandomSatisfiedChannel` 两处调用传入 clientKey;新增 `GetPollingClientKey`)
- Test: `service/channel_select_polling_test.go`

**Interfaces:**
- Consumes: `model.selectChannelByPolling`(Task 5)、`setting.ChannelPollingScope`、`common.GetContextKeyInt`、`constant.ContextKeyTokenId`、`common.GetContextKeyString(c, constant.ContextKeyUserGroup)`。
- Produces:
  - `model.GetRandomSatisfiedChannelWithClient(group, modelName string, retry int, filters []dto.ChannelFilter, pollingClientKey string) (*Channel, error)`
  - `service.GetPollingClientKey(c *gin.Context) string`(返回 `""` 表示 c 为 nil)
  - 原 `model.GetRandomSatisfiedChannel(group, model string, retry int, filters []dto.ChannelFilter)` 签名与行为不变(委托包装)。

- [ ] **Step 1: 写失败的 clientKey 测试**

创建 `service/channel_select_polling_test.go`:

```go
package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPollingScopeFixture(t *testing.T, scope string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	orig := setting.ChannelPollingScope
	setting.ChannelPollingScope = scope
	t.Cleanup(func() { setting.ChannelPollingScope = orig })
}

func TestGetPollingClientKeyUserScope(t *testing.T) {
	setupPollingScopeFixture(t, "user")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set("id", 7)
	common.SetContextKey(c, constant.ContextKeyTokenId, 999)

	assert.Equal(t, "u:7", GetPollingClientKey(c))
}

func TestGetPollingClientKeyTokenScope(t *testing.T) {
	setupPollingScopeFixture(t, "token")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set("id", 7)
	common.SetContextKey(c, constant.ContextKeyTokenId, 999)

	assert.Equal(t, "t:999", GetPollingClientKey(c))
}

func TestGetPollingClientKeyNilContext(t *testing.T) {
	require.Equal(t, "", GetPollingClientKey(nil))
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./service/ -run TestGetPollingClientKey -v`
Expected: FAIL,`undefined: GetPollingClientKey`

- [ ] **Step 3: 实现 service 侧**

在 `service/channel_select.go` 的 `RetryParam` 定义之前插入:

```go
// GetPollingClientKey 返回渠道轮询的客户端 key;"user" 维度用 user id,"token" 维度用 token id。
// c 为 nil 时返回空串(调用方不启用轮询)。
func GetPollingClientKey(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if setting.ChannelPollingScope == setting.ChannelPollingScopeToken {
		return "t:" + strconv.Itoa(common.GetContextKeyInt(c, constant.ContextKeyTokenId))
	}
	return "u:" + strconv.Itoa(c.GetInt("id"))
}
```

补 import:`"strconv"`、`"github.com/QuantumNous/new-api/setting"`(若该文件尚未引入)。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./service/ -run TestGetPollingClientKey -v`
Expected: PASS(3 个测试)

- [ ] **Step 5: model 侧变体重构**

在 `model/channel_cache.go` 中:

5a. 将现有 `GetRandomSatisfiedChannel` 函数体重命名搬入新函数,签名:

```go
func GetRandomSatisfiedChannelWithClient(
	group string,
	model string,
	retry int,
	filters []dto.ChannelFilter,
	pollingClientKey string,
) (*Channel, error)
```

原函数体唯一改动:在选出 `targetChannels` 之后、加权随机代码块(`smoothingFactor := 1` 起)之前插入:

```go
	// 加权轮询分支:启用且带 clientKey 时在同优先级候选内轮流选择
	if channel := selectChannelByPolling(pollingClientKey, model, targetChannels); channel != nil {
		return channel, nil
	}
```

5b. 原位置保留兼容包装(原签名与行为完全不变,含 `MemoryCacheEnabled=false` 直查库路径):

```go
func GetRandomSatisfiedChannel(
	group string,
	model string,
	retry int,
	filters []dto.ChannelFilter,
) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannel(group, model, retry, filters)
	}
	return GetRandomSatisfiedChannelWithClient(group, model, retry, filters, "")
}
```

注意:原函数开头的 `if !common.MemoryCacheEnabled` 判断保留在包装内,`WithClient` 的函数体从 `channelSyncLock.RLock()` 开始(即原函数第二行起)。

- [ ] **Step 6: service 调用点传入 clientKey**

在 `service/channel_select.go` 的 `CacheGetRandomSatisfiedChannel` 中:

6a. 函数体开头(`selectGroup := param.TokenGroup` 之前或之后)加:

```go
	pollingClientKey := GetPollingClientKey(param.Ctx)
```

6b. auto 分支调用(`channel, _ = model.GetRandomSatisfiedChannel(`)改为 `model.GetRandomSatisfiedChannelWithClient(autoGroup, param.ModelName, priorityRetry, filters, pollingClientKey)`,返回值处理不变。

6c. 非 auto 分支调用改为 `model.GetRandomSatisfiedChannelWithClient(param.TokenGroup, param.ModelName, param.GetRetry(), filters, pollingClientKey)`。

- [ ] **Step 7: 全量验证 + 提交**

Run: `gofmt -l model/channel_cache.go service/channel_select.go`(输出须为空)
Run: `go build ./... && go test ./model/ ./service/ -run "TestSelectChannelByPolling|TestGetPollingClientKey" -v && go test ./service/ -run TestChannelSelect -v`
Expected: PASS(含既有 `channel_select_auto_groups_test.go` 回归)

```bash
git add model/channel_cache.go service/channel_select.go service/channel_select_polling_test.go
git commit -m "feat(relay): per-client weighted round-robin channel selection behind flag"
```

### Task 7: 前端「渠道轮询」配置 UI

**Files:**
- Create: `web/src/features/system-settings/request-limits/channel-polling-section.tsx`
- Modify: `web/src/features/system-settings/security/section-registry.tsx`
- Modify: `web/src/features/system-settings/types.ts`
- Modify: `web/src/i18n/locales/zh.json`

**Interfaces:**
- Consumes: options `ChannelPollingEnabled` / `ChannelPollingScope`;同 Task 4 的组件套件。
- Produces: 管理员配置 UI。

- [ ] **Step 1: types.ts 加字段**(与 Task 4 同接口内)

```ts
ChannelPollingEnabled: boolean
ChannelPollingScope: string
```

- [ ] **Step 2: 新建组件**(结构仿 Task 4 组件;仅开关 + 下拉两项,复用其 import 集与 `onSubmit` 模式)

```tsx
/* 版权头同 Task 4 组件(AGPL 头,逐字复制) */
import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
} from '@/components/ui/form'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const createSchema = () =>
  z.object({
    ChannelPollingEnabled: z.boolean(),
    ChannelPollingScope: z.enum(['user', 'token']),
  })

type FormValues = z.infer<ReturnType<typeof createSchema>>

export function ChannelPollingSection({
  settings,
  refetch,
}: {
  settings: Partial<FormValues> & Record<string, any>
  refetch: () => void
}) {
  const { t } = useTranslation()
  const form = useForm<FormValues>({
    resolver: zodResolver(createSchema()),
    defaultValues: { ChannelPollingEnabled: false, ChannelPollingScope: 'user' },
  })
  const updateOption = useUpdateOption()

  useEffect(() => {
    form.reset({
      ChannelPollingEnabled: Boolean(settings.ChannelPollingEnabled),
      ChannelPollingScope:
        settings.ChannelPollingScope === 'token' ? 'token' : 'user',
    })
  }, [settings, form])

  const onSubmit = (values: FormValues) => {
    updateOption('ChannelPollingEnabled', String(values.ChannelPollingEnabled))
    updateOption('ChannelPollingScope', values.ChannelPollingScope)
    refetch()
  }

  return (
    <SettingsSection
      title={t('Channel Polling')}
      description={t(
        'Distribute requests across channels round-robin by weight for each client and model',
      )}
    >
      <SettingsForm form={form} onSubmit={onSubmit}>
        <FormField
          control={form.control}
          name='ChannelPollingEnabled'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Enable Channel Polling')}</FormLabel>
              <FormControl>
                <Switch
                  checked={field.value}
                  onCheckedChange={(checked) => field.onChange(checked)}
                />
              </FormControl>
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='ChannelPollingScope'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Client Dimension')}</FormLabel>
              <Select onValueChange={field.onChange} value={field.value}>
                <FormControl>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                </FormControl>
                <SelectContent>
                  <SelectItem value='user'>{t('Per User')}</SelectItem>
                  <SelectItem value='token'>{t('Per Token')}</SelectItem>
                </SelectContent>
              </Select>
            </FormItem>
          )}
        />
        <SettingsPageFormActions />
      </SettingsForm>
    </SettingsSection>
  )
}
```

- [ ] **Step 3: 注册 section + i18n**

`section-registry.tsx` 仿 Task 4 步骤 3 注册 `id: 'channel-polling'`,settings 传 `ChannelPollingEnabled` / `ChannelPollingScope`。

`zh.json` 追加:

```json
"Channel Polling": "渠道轮询",
"Distribute requests across channels round-robin by weight for each client and model": "按客户端+模型对可用渠道按权重轮流分发",
"Enable Channel Polling": "启用渠道轮询"
```

(`Client Dimension`/`Per User`/`Per Token` 已在 Task 4 添加,不重复。)

- [ ] **Step 4: 构建验证 + 提交**

Run: `cd web && bun run build`
Expected: 构建成功

```bash
git add web/src/features/system-settings/ web/src/i18n/locales/zh.json
git commit -m "feat(web): channel polling settings section"
```

---

## 模块 3:渠道错误关键字规则引擎

### Task 8: 规则配置与校验

**Files:**
- Create: `setting/operation_setting/channel_error_actions.go`
- Modify: `model/option.go`(两处插入)
- Test: `setting/operation_setting/channel_error_actions_test.go`

**Interfaces:**
- Consumes: `common.Marshal` / `common.Unmarshal`。
- Produces(后续任务依赖,签名逐字):
  - `operation_setting.ChannelErrorAction`(string 类型;常量 `ChannelErrorActionDisable`/`ChannelErrorActionRetryNext`/`ChannelErrorActionPassthrough`)
  - `operation_setting.ChannelErrorKeywordRule{Keywords []string; Action ChannelErrorAction}`
  - `operation_setting.ChannelErrorKeywordActions []ChannelErrorKeywordRule`
  - `operation_setting.ChannelErrorKeywordActions2JSONString() string`
  - `operation_setting.UpdateChannelErrorKeywordActionsByJSONString(jsonStr string) error`

- [ ] **Step 1: 写失败的配置测试**

创建 `setting/operation_setting/channel_error_actions_test.go`:

```go
package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelErrorKeywordActionsDefaultsValid(t *testing.T) {
	// 默认规则必须通过自身校验且动作合法
	require.NotEmpty(t, ChannelErrorKeywordActions)
	for _, rule := range ChannelErrorKeywordActions {
		assert.NotEmpty(t, rule.Keywords)
		assert.Contains(t,
			[]ChannelErrorAction{ChannelErrorActionDisable, ChannelErrorActionRetryNext, ChannelErrorActionPassthrough},
			rule.Action,
		)
	}
}

func TestUpdateChannelErrorKeywordActionsValid(t *testing.T) {
	original := ChannelErrorKeywordActions
	t.Cleanup(func() { ChannelErrorKeywordActions = original })

	err := UpdateChannelErrorKeywordActionsByJSONString(
		`[{"keywords":["boom"],"action":"disable"}]`)
	require.NoError(t, err)
	require.Len(t, ChannelErrorKeywordActions, 1)
	assert.Equal(t, []string{"boom"}, ChannelErrorKeywordActions[0].Keywords)
	assert.Equal(t, ChannelErrorActionDisable, ChannelErrorKeywordActions[0].Action)
}

func TestUpdateChannelErrorKeywordActionsRejectsInvalidAction(t *testing.T) {
	err := UpdateChannelErrorKeywordActionsByJSONString(
		`[{"keywords":["x"],"action":"explode"}]`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid action")
}

func TestUpdateChannelErrorKeywordActionsRejectsEmptyKeywords(t *testing.T) {
	err := UpdateChannelErrorKeywordActionsByJSONString(
		`[{"keywords":[],"action":"disable"}]`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no keywords")
}

func TestChannelErrorKeywordActions2JSONStringRoundTrip(t *testing.T) {
	original := ChannelErrorKeywordActions
	t.Cleanup(func() { ChannelErrorKeywordActions = original })

	require.NoError(t, UpdateChannelErrorKeywordActionsByJSONString(
		`[{"keywords":["a"],"action":"passthrough"}]`))
	var rules []ChannelErrorKeywordRule
	require.NoError(t, common.Unmarshal([]byte(ChannelErrorKeywordActions2JSONString()), &rules))
	require.Len(t, rules, 1)
	assert.Equal(t, ChannelErrorActionPassthrough, rules[0].Action)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./setting/operation_setting/ -run TestChannelErrorKeywordActions -v`
Expected: FAIL,`undefined: ChannelErrorKeywordActions`

- [ ] **Step 3: 实现 `setting/operation_setting/channel_error_actions.go`**

```go
package operation_setting

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
)

// ChannelErrorAction 是渠道错误关键字的处置动作。
type ChannelErrorAction string

const (
	ChannelErrorActionDisable     ChannelErrorAction = "disable"     // 禁用渠道
	ChannelErrorActionRetryNext   ChannelErrorAction = "retry_next"  // 不禁用,换下一渠道重试
	ChannelErrorActionPassthrough ChannelErrorAction = "passthrough" // 不禁用不重试,原样返回
)

// ChannelErrorKeywordRule 按序匹配,首条命中生效。
type ChannelErrorKeywordRule struct {
	Keywords []string           `json:"keywords"`
	Action   ChannelErrorAction `json:"action"`
}

// 默认规则:余额类禁用、限流类换渠道、内容安全类原样透传。
var defaultChannelErrorKeywordActions = []ChannelErrorKeywordRule{
	{Keywords: []string{"余额不足", "无可用资源包", "insufficient quota"}, Action: ChannelErrorActionDisable},
	{Keywords: []string{"限流", "rate limit", "too many requests"}, Action: ChannelErrorActionRetryNext},
	{Keywords: []string{"内容安全", "sensitive content"}, Action: ChannelErrorActionPassthrough},
}

var ChannelErrorKeywordActions = defaultChannelErrorKeywordActions

func ChannelErrorKeywordActions2JSONString() string {
	jsonBytes, err := common.Marshal(ChannelErrorKeywordActions)
	if err != nil {
		common.SysLog("error marshalling channel error keyword actions: " + err.Error())
		return "[]"
	}
	return string(jsonBytes)
}

func UpdateChannelErrorKeywordActionsByJSONString(jsonStr string) error {
	rules := make([]ChannelErrorKeywordRule, 0)
	if err := common.Unmarshal([]byte(jsonStr), &rules); err != nil {
		return err
	}
	for i, rule := range rules {
		if len(rule.Keywords) == 0 {
			return fmt.Errorf("rule %d has no keywords", i)
		}
		switch rule.Action {
		case ChannelErrorActionDisable, ChannelErrorActionRetryNext, ChannelErrorActionPassthrough:
		default:
			return fmt.Errorf("rule %d has invalid action: %s", i, rule.Action)
		}
	}
	ChannelErrorKeywordActions = rules
	return nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./setting/operation_setting/ -run TestChannelErrorKeywordActions -v`
Expected: PASS(5 个测试)

- [ ] **Step 5: option 注册(两处)**

5a. 默认值注册 —— `model/option.go` 锚点 `common.OptionMap["ChannelPollingScope"] = setting.ChannelPollingScope`(Task 5 插入块末行)之后:

```go
common.OptionMap["ChannelErrorKeywordActions"] = operation_setting.ChannelErrorKeywordActions2JSONString()
```

5b. 字符串分支 —— 锚点 `case "ChannelPollingScope":`(Task 5 插入块)之后:

```go
	case "ChannelErrorKeywordActions":
		err = operation_setting.UpdateChannelErrorKeywordActionsByJSONString(value)
```

(`model/option.go` 已 import `operation_setting`,见 `case "DemoSiteEnabled"` 等现有用法。)

- [ ] **Step 6: 全量验证 + 提交**

Run: `gofmt -l setting/operation_setting/channel_error_actions.go model/option.go`(输出须为空)
Run: `go build ./... && go test ./setting/operation_setting/ -run TestChannelErrorKeywordActions -v`

```bash
git add setting/operation_setting/channel_error_actions.go setting/operation_setting/channel_error_actions_test.go model/option.go
git commit -m "feat(setting): channel error keyword action rules with validation"
```

### Task 9: 规则匹配引擎与错误链路挂接

**Files:**
- Create: `service/channel_error_rules.go`
- Modify: `service/channel.go`(`ShouldDisableChannel` 开头,`service/channel.go:57`)
- Modify: `service/relay_error.go`(`ShouldRetryRelayError` 开头,`service/relay_error.go:19`)
- Test: `service/channel_error_rules_test.go`

**Interfaces:**
- Consumes: Task 8 全部 `operation_setting.ChannelError*`;`service.AcSearch(findText string, dict []string, stopImmediately bool) (bool, []string)`(`service/str.go:132`);`types.NewAPIError`。
- Produces: `service.MatchChannelErrorAction(err *types.NewAPIError) (operation_setting.ChannelErrorAction, bool)`。

- [ ] **Step 1: 写失败的匹配与挂接测试**

创建 `service/channel_error_rules_test.go`:

```go
package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupChannelErrorRulesFixture(t *testing.T, rules []operation_setting.ChannelErrorKeywordRule) {
	t.Helper()
	origRules := operation_setting.ChannelErrorKeywordActions
	origAutoEnabled := common.AutomaticDisableChannelEnabled
	origKeywords := operation_setting.AutomaticDisableKeywords
	operation_setting.ChannelErrorKeywordActions = rules
	t.Cleanup(func() {
		operation_setting.ChannelErrorKeywordActions = origRules
		common.AutomaticDisableChannelEnabled = origAutoEnabled
		operation_setting.AutomaticDisableKeywords = origKeywords
	})
}

// newRetryTestContext 构造 ShouldRetryRelayError 所需的最小 gin context。
func newRetryTestContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return c
}

func newRelayErr(message string, statusCode int) *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodeUpdateDataError, statusCode)
}

var threeActionRules = []operation_setting.ChannelErrorKeywordRule{
	{Keywords: []string{"余额不足", "insufficient quota"}, Action: operation_setting.ChannelErrorActionDisable},
	{Keywords: []string{"限流", "rate limit"}, Action: operation_setting.ChannelErrorActionRetryNext},
	{Keywords: []string{"内容安全"}, Action: operation_setting.ChannelErrorActionPassthrough},
}

func TestMatchChannelErrorActionFirstMatchWins(t *testing.T) {
	setupChannelErrorRulesFixture(t, threeActionRules)

	action, ok := MatchChannelErrorAction(newRelayErr("上游返回:余额不足或无可用资源包,请充值。", http.StatusForbidden))
	require.True(t, ok)
	assert.Equal(t, operation_setting.ChannelErrorActionDisable, action)

	action, ok = MatchChannelErrorAction(newRelayErr("rate limit exceeded", http.StatusTooManyRequests))
	require.True(t, ok)
	assert.Equal(t, operation_setting.ChannelErrorActionRetryNext, action)
}

func TestMatchChannelErrorActionNoMatch(t *testing.T) {
	setupChannelErrorRulesFixture(t, threeActionRules)

	_, ok := MatchChannelErrorAction(newRelayErr("connection reset by peer", http.StatusBadGateway))
	assert.False(t, ok)
}

func TestMatchChannelErrorActionNilOrEmptyRules(t *testing.T) {
	setupChannelErrorRulesFixture(t, nil)
	_, ok := MatchChannelErrorAction(newRelayErr("anything", http.StatusInternalServerError))
	assert.False(t, ok)

	_, ok = MatchChannelErrorAction(nil)
	assert.False(t, ok)
}

func TestDisableActionShortCircuitsShouldDisableChannel(t *testing.T) {
	setupChannelErrorRulesFixture(t, threeActionRules)
	common.AutomaticDisableChannelEnabled = false // 规则命中时不依赖全局开关

	assert.True(t, ShouldDisableChannel(newRelayErr("余额不足", http.StatusForbidden)))
}

func TestRetryNextActionForcesRetryAndSkipsDisable(t *testing.T) {
	setupChannelErrorRulesFixture(t, threeActionRules)
	common.AutomaticDisableChannelEnabled = true

	err := newRelayErr("请求触发限流", http.StatusTooManyRequests)
	assert.False(t, ShouldDisableChannel(err))
	assert.True(t, ShouldRetryRelayError(newRetryTestContext(t), err, 1))
}

func TestPassthroughActionBlocksRetryAndDisable(t *testing.T) {
	setupChannelErrorRulesFixture(t, threeActionRules)
	common.AutomaticDisableChannelEnabled = true

	err := newRelayErr("内容安全审核未通过", http.StatusBadRequest)
	assert.False(t, ShouldDisableChannel(err))
	assert.False(t, ShouldRetryRelayError(newRetryTestContext(t), err, 1))
}

func TestUnmatchedErrorFallsBackToExistingLogic(t *testing.T) {
	setupChannelErrorRulesFixture(t, threeActionRules)
	common.AutomaticDisableChannelEnabled = true
	operation_setting.AutomaticDisableKeywords = []string{"provider exploded"}

	err := newRelayErr("provider exploded upstream", http.StatusInternalServerError)
	assert.True(t, ShouldDisableChannel(err), "falls back to AutomaticDisableKeywords")
	assert.True(t, ShouldRetryRelayError(newRetryTestContext(t), err, 1), "falls back to status-code retry rules")
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./service/ -run "TestMatchChannelErrorAction|TestDisableAction|TestRetryNextAction|TestPassthroughAction|TestUnmatchedError" -v`
Expected: FAIL,`undefined: MatchChannelErrorAction`

- [ ] **Step 3: 实现 `service/channel_error_rules.go`**

```go
package service

import (
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// MatchChannelErrorAction 按规则表顺序匹配错误消息,返回首条命中规则的动作。
// 匹配输入为小写化错误文本,检索复用 AC 自动机 AcSearch;无规则或未命中返回 ok=false。
func MatchChannelErrorAction(err *types.NewAPIError) (operation_setting.ChannelErrorAction, bool) {
	if err == nil || len(operation_setting.ChannelErrorKeywordActions) == 0 {
		return "", false
	}
	lowerMessage := strings.ToLower(err.Error())
	for _, rule := range operation_setting.ChannelErrorKeywordActions {
		matched, _ := AcSearch(lowerMessage, rule.Keywords, true)
		if matched {
			return rule.Action, true
		}
	}
	return "", false
}
```

- [ ] **Step 4: 挂接两个判定函数**

4a. `service/channel.go` `ShouldDisableChannel`:在 `if err == nil { return false }` 之后、`if types.IsChannelError(err)` 之前插入:

```go
	// 错误关键字规则引擎:命中即短路,未命中回落现有逻辑
	if action, ok := MatchChannelErrorAction(err); ok {
		return action == operation_setting.ChannelErrorActionDisable
	}
```

(`service/channel.go` 已 import `operation_setting`,见现有 `ShouldDisableByStatusCode` 调用。)

4b. `service/relay_error.go` `ShouldRetryRelayError`:在 `if openaiErr == nil { return false }` 之后、`if ShouldSkipRetryAfterChannelAffinityFailure(c)` 之前插入:

```go
	// 错误关键字规则引擎:命中即短路,未命中回落现有逻辑
	if action, ok := MatchChannelErrorAction(openaiErr); ok {
		switch action {
		case operation_setting.ChannelErrorActionRetryNext:
			return true
		case operation_setting.ChannelErrorActionPassthrough:
			return false
		}
		// disable:落到现有重试判定(渠道错误/状态码规则通常已允许换渠道)
	}
```

(`service/relay_error.go` 若未 import `operation_setting` 则补充。)

- [ ] **Step 5: 运行确认通过**

Run: `gofmt -w service/channel_error_rules.go service/channel.go service/relay_error.go && go build ./... && go test ./service/ -run "TestMatchChannelErrorAction|TestDisableAction|TestRetryNextAction|TestPassthroughAction|TestUnmatchedError|TestChannelSelect" -v`
Expected: PASS(新增 7 个 + 既有 `channel_select_auto_groups_test.go` 回归)

- [ ] **Step 6: 提交**

```bash
git add service/channel_error_rules.go service/channel_error_rules_test.go service/channel.go service/relay_error.go
git commit -m "feat(service): channel error keyword rule engine with disable/retry_next/passthrough"
```

### Task 10: 前端「错误关键字规则」编辑 UI

**Files:**
- Create: `web/src/features/system-settings/request-limits/channel-error-rules-section.tsx`
- Modify: `web/src/features/system-settings/security/section-registry.tsx`
- Modify: `web/src/features/system-settings/types.ts`
- Modify: `web/src/i18n/locales/zh.json`

**Interfaces:**
- Consumes: option `ChannelErrorKeywordActions`(JSON 字符串);同 Task 4 组件套件。
- Produces: 管理员规则编辑 UI(JSON 编辑,首版不做可视化行编辑器)。

- [ ] **Step 1: types.ts 加字段**

```ts
ChannelErrorKeywordActions: string
```

- [ ] **Step 2: 新建组件**(骨架与 Task 4 相同:AGPL 版权头、`useTranslation`、`SettingsForm`/`SettingsSection`/`useUpdateOption`;差异部分如下)

schema 与校验:

```tsx
const isValidRulesJSON = (value: string | undefined) => {
  if (!value || value.trim() === '') return true
  try {
    const parsed: unknown = JSON.parse(value)
    if (!Array.isArray(parsed)) return false
    for (const rule of parsed) {
      if (
        typeof rule !== 'object' ||
        rule === null ||
        !Array.isArray((rule as { keywords?: unknown }).keywords) ||
        (rule as { keywords: unknown[] }).keywords.length === 0 ||
        !['disable', 'retry_next', 'passthrough'].includes(
          (rule as { action?: unknown }).action as string,
        )
      ) {
        return false
      }
    }
    return true
  } catch {
    return false
  }
}

const createSchema = (t: (key: string) => string) =>
  z.object({
    ChannelErrorKeywordActions: z
      .string()
      .optional()
      .refine(isValidRulesJSON, {
        message: t('Invalid JSON format or values out of allowed range'),
      }),
  })
```

表单体(单个 JSON 编辑字段):

```tsx
<FormField
  control={form.control}
  name='ChannelErrorKeywordActions'
  render={({ field }) => (
    <FormItem>
      <FormLabel>{t('Channel Error Rules')}</FormLabel>
      <FormControl>
        <JsonCodeEditor
          value={field.value ?? ''}
          onChange={field.onChange}
          placeholder='[{"keywords":["余额不足"],"action":"disable"}]'
        />
      </FormControl>
      <FormDescription>
        {t(
          'Ordered rules; first match wins. Actions: disable, retry_next, passthrough',
        )}
      </FormDescription>
      <FormMessage />
    </FormItem>
  )}
/>
```

`onSubmit`:`updateOption('ChannelErrorKeywordActions', values.ChannelErrorKeywordActions ?? '[]')`;section 标题 `t('Channel Error Rules')`,描述 `t('Classify upstream errors by keywords and choose disable, retry-next, or passthrough')`。

- [ ] **Step 3: 注册 section + i18n**

`section-registry.tsx` 仿前注册 `id: 'channel-error-rules'`,settings 传 `ChannelErrorKeywordActions`。

`zh.json` 追加:

```json
"Channel Error Rules": "渠道错误规则",
"Classify upstream errors by keywords and choose disable, retry-next, or passthrough": "按关键字识别上游错误并选择禁用、换渠道重试或原样透传",
"Ordered rules; first match wins. Actions: disable, retry_next, passthrough": "规则按序匹配,首条命中生效;动作:disable(禁用)、retry_next(换渠道重试)、passthrough(原样透传)"
```

- [ ] **Step 4: 构建验证 + 提交**

Run: `cd web && bun run build`
Expected: 构建成功

```bash
git add web/src/features/system-settings/ web/src/i18n/locales/zh.json
git commit -m "feat(web): channel error rules settings section"
```
