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
