package setting

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"gopkg.in/yaml.v3"
)

// CustomConfig 是 custom-config.yaml 的结构：本分支全部定制功能共用这一个
// 配置文件。区块或字段缺省时保持对应 setting var 的内置默认值；文件存在
// 但解析或校验失败时返回错误，由调用方在启动阶段 FatalLog，禁止静默降级。
type CustomConfig struct {
	ConcurrencyQueue struct {
		Enabled        bool           `yaml:"enabled"`
		Scope          string         `yaml:"scope"`           // user | token，缺省 user
		DefaultLimit   *int           `yaml:"default_limit"`   // 缺省保持内置默认 2；显式 0 = 不限；负数非法
		TimeoutSeconds *int           `yaml:"timeout_seconds"` // 缺省保持内置默认 30；必须 > 0
		GroupLimits    map[string]int `yaml:"group_limits"`    // 缺省保持内置默认；值 0 = 该分组不限
	} `yaml:"concurrency_queue"`
	ChannelPolling struct {
		Enabled bool   `yaml:"enabled"`
		Scope   string `yaml:"scope"` // user | token，缺省 user
	} `yaml:"channel_polling"`
	ChannelErrorRules struct {
		// Rules 为空时保留内置默认规则（见 channel_error_actions.go）
		Rules []operation_setting.ChannelErrorKeywordRule `yaml:"rules"`
	} `yaml:"channel_error_rules"`
	TokenGroupOverride struct {
		Disable bool `yaml:"disable"`
	} `yaml:"token_group_override"`
	ClaudeMissingThinkingPatch struct {
		// 仅 GLM 风格 Anthropic 兼容上游可列入；官方 Anthropic 渠道禁止
		ChannelIDs []int `yaml:"channel_ids"`
	} `yaml:"claude_missing_thinking_patch"`
}

// PatchMissingThinkingChannels holds the channel IDs listed in
// claude_missing_thinking_patch.channel_ids, built by LoadCustomConfig. Empty
// map means no channel is patched. relay/channel/claude reads it per request.
var PatchMissingThinkingChannels = map[int]struct{}{}

// DefaultCustomConfigPath returns the config path: the CUSTOM_CONFIG_PATH
// environment variable when set, otherwise ./custom-config.yaml.
func DefaultCustomConfigPath() string {
	if path := os.Getenv("CUSTOM_CONFIG_PATH"); path != "" {
		return path
	}
	return "custom-config.yaml"
}

// LoadCustomConfig reads and validates the unified custom config file and
// applies it to the setting variables. A missing file keeps every built-in
// default and is not an error; any other read, parse, or validation failure
// returns an error so the caller can abort startup.
func LoadCustomConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			common.SysLog(common.LogText("%s not found, custom features keep built-in defaults", path))
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	var config CustomConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if err := config.Apply(); err != nil {
		return fmt.Errorf("invalid %s: %w", path, err)
	}
	common.SysLog(common.LogText("loaded custom config from %s", path))
	return nil
}

// Apply validates the parsed config and assigns it to the setting variables.
// Zero-valued optional fields keep the built-in defaults; structurally invalid
// values (negative limits, unknown enums, empty keywords) are rejected.
func (c *CustomConfig) Apply() error {
	queue := c.ConcurrencyQueue
	if queue.Scope == "" {
		queue.Scope = ConcurrencyQueueScopeUser
	}
	if queue.Scope != ConcurrencyQueueScopeUser && queue.Scope != ConcurrencyQueueScopeToken {
		return fmt.Errorf("concurrency_queue.scope must be %q or %q, got %q", ConcurrencyQueueScopeUser, ConcurrencyQueueScopeToken, queue.Scope)
	}
	if queue.DefaultLimit != nil && *queue.DefaultLimit < 0 {
		return fmt.Errorf("concurrency_queue.default_limit must be >= 0, got %d", *queue.DefaultLimit)
	}
	if queue.TimeoutSeconds != nil && *queue.TimeoutSeconds <= 0 {
		return fmt.Errorf("concurrency_queue.timeout_seconds must be > 0, got %d", *queue.TimeoutSeconds)
	}
	if queue.GroupLimits != nil {
		for group, limit := range queue.GroupLimits {
			if limit < 0 {
				return fmt.Errorf("concurrency_queue.group_limits[%s] must be >= 0, got %d", group, limit)
			}
		}
	}

	polling := c.ChannelPolling
	if polling.Scope == "" {
		polling.Scope = ChannelPollingScopeUser
	}
	if polling.Scope != ChannelPollingScopeUser && polling.Scope != ChannelPollingScopeToken {
		return fmt.Errorf("channel_polling.scope must be %q or %q, got %q", ChannelPollingScopeUser, ChannelPollingScopeToken, polling.Scope)
	}

	for i, rule := range c.ChannelErrorRules.Rules {
		if len(rule.Keywords) == 0 {
			return fmt.Errorf("channel_error_rules.rules[%d] has no keywords", i)
		}
		switch rule.Action {
		case operation_setting.ChannelErrorActionDisable, operation_setting.ChannelErrorActionRetryNext, operation_setting.ChannelErrorActionPassthrough:
		default:
			return fmt.Errorf("channel_error_rules.rules[%d] has invalid action: %s", i, rule.Action)
		}
	}

	ConcurrencyQueueEnabled = queue.Enabled
	ConcurrencyQueueScope = queue.Scope
	if queue.DefaultLimit != nil {
		ConcurrencyQueueDefaultLimit = *queue.DefaultLimit
	}
	if queue.TimeoutSeconds != nil {
		ConcurrencyQueueTimeoutSeconds = *queue.TimeoutSeconds
	}
	if queue.GroupLimits != nil {
		ConcurrencyQueueMutex.Lock()
		ConcurrencyQueueGroupLimit = queue.GroupLimits
		ConcurrencyQueueMutex.Unlock()
	}

	ChannelPollingEnabled = polling.Enabled
	ChannelPollingScope = polling.Scope

	if len(c.ChannelErrorRules.Rules) > 0 {
		operation_setting.ChannelErrorKeywordActions = c.ChannelErrorRules.Rules
	}

	DisableTokenGroupOverride = c.TokenGroupOverride.Disable

	channels := make(map[int]struct{}, len(c.ClaudeMissingThinkingPatch.ChannelIDs))
	for _, id := range c.ClaudeMissingThinkingPatch.ChannelIDs {
		channels[id] = struct{}{}
	}
	PatchMissingThinkingChannels = channels
	return nil
}
