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
