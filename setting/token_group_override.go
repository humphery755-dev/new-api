package setting

// DisableTokenGroupOverride 为 true 时，api-key（token）的分组不再覆盖账户分组，
// 渠道分发强制按用户账户分配的分组（ContextKeyUsingGroup = 账户分组）进行。
// 默认 false，保持原有行为：token 设置了分组则优先用 token 分组。
var DisableTokenGroupOverride = false
