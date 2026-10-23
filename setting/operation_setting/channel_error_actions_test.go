package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelErrorKeywordActionsDefaultsValid(t *testing.T) {
	// 默认规则必须通过自身校验且动作合法（custom-config.yaml 未配置 rules 时使用）
	require.NotEmpty(t, ChannelErrorKeywordActions)
	for _, rule := range ChannelErrorKeywordActions {
		assert.NotEmpty(t, rule.Keywords)
		assert.Contains(t,
			[]ChannelErrorAction{ChannelErrorActionDisable, ChannelErrorActionRetryNext, ChannelErrorActionPassthrough},
			rule.Action,
		)
	}
}
