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
