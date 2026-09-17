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
