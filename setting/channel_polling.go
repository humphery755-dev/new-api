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
