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
