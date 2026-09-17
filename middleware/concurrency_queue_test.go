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

// performConcurrencyQueueRequest 以指定身份(userId/tokenId/tokenGroup)构造
// [身份注入, ConcurrencyQueue(), handler] 真实 handler 链并同步执行一次请求。
// gin v1.9.1 的 Context 没有导出 SetHandlers,因此经路由注册后由 ServeHTTP 组链。
func performConcurrencyQueueRequest(handler gin.HandlerFunc, userId, tokenId int, tokenGroup string, req *http.Request) *httptest.ResponseRecorder {
	router := gin.New()
	router.POST("/v1/chat/completions",
		func(c *gin.Context) {
			c.Set("id", userId)
			common.SetContextKey(c, constant.ContextKeyTokenId, tokenId)
			common.SetContextKey(c, constant.ContextKeyTokenGroup, tokenGroup)
		},
		ConcurrencyQueue(),
		handler,
	)
	if req == nil {
		req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestConcurrencyQueueDisabledPassesThrough(t *testing.T) {
	setupConcurrencyQueueFixture(t, false, "user", 1, 30, nil)
	w := performConcurrencyQueueRequest(func(c *gin.Context) { c.Status(http.StatusOK) }, 1, 0, "default", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, len(concurrencyQueues))
}

func TestConcurrencyQueueThirdRequestQueuesUntilRelease(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "user", 2, 30, nil)

	// 中间件必须与业务 handler 组成真实链执行(路由注册 + ServeHTTP),
	// 单独调用 mw(c) 会在 mw 内部跑完空链并立即释放槽位,无法测排队。
	block := make(chan struct{})
	handler := func(c *gin.Context) {
		<-block
		c.Status(http.StatusOK)
	}

	done1 := make(chan struct{})
	go func() {
		performConcurrencyQueueRequest(handler, 1, 0, "default", nil)
		close(done1)
	}()
	waitForSlots(t, "u:1", 1)

	done2 := make(chan struct{})
	go func() {
		performConcurrencyQueueRequest(handler, 1, 0, "default", nil)
		close(done2)
	}()
	waitForSlots(t, "u:1", 2)

	done3 := make(chan struct{})
	go func() {
		performConcurrencyQueueRequest(handler, 1, 0, "default", nil)
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

	hold := make(chan struct{})
	handler := func(c *gin.Context) {
		<-hold
		c.Status(http.StatusOK)
	}

	done1 := make(chan struct{})
	go func() {
		performConcurrencyQueueRequest(handler, 1, 0, "default", nil)
		close(done1)
	}()
	waitForSlots(t, "u:1", 1)

	// c2 无业务 handler(空链等价):排队超时由中间件自身返回 429
	w2 := performConcurrencyQueueRequest(func(c *gin.Context) { c.Status(http.StatusOK) }, 1, 0, "default", nil)

	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
	assert.Equal(t, "1", w2.Header().Get("Retry-After"))
	close(hold)
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("first request never finished")
	}
	waitForCondition(t, func() bool { return len(concurrencyQueues) == 0 })
}

func TestConcurrencyQueueWebSocketUpgradePassesThrough(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "user", 1, 30, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Upgrade", "websocket")
	w := performConcurrencyQueueRequest(func(c *gin.Context) { c.Status(http.StatusOK) }, 1, 0, "default", req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, len(concurrencyQueues))
}

func TestConcurrencyQueueZeroCapacityPassesThrough(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "user", 0, 30, map[string]int{"default": 0})
	w := performConcurrencyQueueRequest(func(c *gin.Context) { c.Status(http.StatusOK) }, 1, 0, "default", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, len(concurrencyQueues))
}

func TestConcurrencyQueueGroupLimitOverridesDefault(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "user", 3, 1, map[string]int{"vip": 1})

	hold := make(chan struct{})
	handler := func(c *gin.Context) {
		<-hold
		c.Status(http.StatusOK)
	}

	done1 := make(chan struct{})
	go func() {
		performConcurrencyQueueRequest(handler, 1, 0, "vip", nil)
		close(done1)
	}()
	waitForSlots(t, "u:1", 1)

	// c2 无业务 handler(空链等价):分组覆盖容量 1,排队超时由中间件返回 429
	w2 := performConcurrencyQueueRequest(func(c *gin.Context) { c.Status(http.StatusOK) }, 1, 0, "vip", nil)
	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
	close(hold)
	<-done1
}

func TestConcurrencyQueueTokenScopeIsolatesKeys(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "token", 1, 30, nil)

	hold := make(chan struct{})
	handler := func(c *gin.Context) {
		<-hold
		c.Status(http.StatusOK)
	}

	done1 := make(chan struct{})
	go func() {
		performConcurrencyQueueRequest(handler, 1, 100, "default", nil)
		close(done1)
	}()
	waitForSlots(t, "t:100", 1)

	// c2 无业务 handler(空链等价):t:200 容量空闲,应立即通过
	w2 := performConcurrencyQueueRequest(func(c *gin.Context) { c.Status(http.StatusOK) }, 1, 200, "default", nil)
	assert.Equal(t, http.StatusOK, w2.Code, "different token => different queue key")
	// c2 走自己的 key "t:200",完成后其空队列应被回收;t:100 此刻仍被 c1 占用。
	waitForCondition(t, func() bool {
		concurrencyQueuesMu.Lock()
		defer concurrencyQueuesMu.Unlock()
		_, ok := concurrencyQueues["t:200"]
		return !ok
	})
	close(hold)
	<-done1
}

func TestConcurrencyQueueClientDisconnectWhileQueued(t *testing.T) {
	setupConcurrencyQueueFixture(t, true, "user", 1, 30, nil)

	hold := make(chan struct{})
	handler := func(c *gin.Context) {
		<-hold
		c.Status(http.StatusOK)
	}

	done1 := make(chan struct{})
	go func() {
		performConcurrencyQueueRequest(handler, 1, 0, "default", nil)
		close(done1)
	}()
	waitForSlots(t, "u:1", 1)

	// 排队中的请求,客户端断开(context 取消)后必须让出且不写 429
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	done2 := make(chan struct{})
	var w2 *httptest.ResponseRecorder
	go func() {
		w2 = performConcurrencyQueueRequest(handler, 1, 0, "default", req)
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
	assert.Equal(t, 0, w2.Body.Len(), "disconnect path is silent: empty body")

	close(hold)
	<-done1
	waitForCondition(t, func() bool { return len(concurrencyQueues) == 0 })
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
