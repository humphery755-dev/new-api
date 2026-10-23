package operation_setting

// ChannelErrorAction 是渠道错误关键字的处置动作。
type ChannelErrorAction string

const (
	ChannelErrorActionDisable     ChannelErrorAction = "disable"     // 禁用渠道
	ChannelErrorActionRetryNext   ChannelErrorAction = "retry_next"  // 不禁用,换下一渠道重试
	ChannelErrorActionPassthrough ChannelErrorAction = "passthrough" // 不禁用不重试,原样返回
)

// ChannelErrorKeywordRule 按序匹配,首条命中生效。
// custom-config.yaml 的 channel_error_rules.rules 使用同一结构;json tag
// 保留给历史 DB option 值的迁移参考。
type ChannelErrorKeywordRule struct {
	Keywords []string           `json:"keywords"`
	Action   ChannelErrorAction `json:"action"`
}

// 默认规则:余额类禁用、限流类换渠道、内容安全类原样透传。
// custom-config.yaml 未配置 rules 时使用本默认。
var defaultChannelErrorKeywordActions = []ChannelErrorKeywordRule{
	{Keywords: []string{"余额不足", "无可用资源包", "insufficient quota"}, Action: ChannelErrorActionDisable},
	{Keywords: []string{"限流", "rate limit", "too many requests"}, Action: ChannelErrorActionRetryNext},
	{Keywords: []string{"内容安全", "sensitive content"}, Action: ChannelErrorActionPassthrough},
}

var ChannelErrorKeywordActions = defaultChannelErrorKeywordActions
