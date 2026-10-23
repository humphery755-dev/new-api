package setting

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultChannelErrorRulesForTest mirrors the built-in default rules asserted
// by the loader tests: a config without rules must leave them in place.
var defaultChannelErrorRulesForTest = []operation_setting.ChannelErrorKeywordRule{
	{Keywords: []string{"余额不足", "无可用资源包", "insufficient quota"}, Action: operation_setting.ChannelErrorActionDisable},
	{Keywords: []string{"限流", "rate limit", "too many requests"}, Action: operation_setting.ChannelErrorActionRetryNext},
	{Keywords: []string{"内容安全", "sensitive content"}, Action: operation_setting.ChannelErrorActionPassthrough},
}

// saveCustomSettingVars snapshots every package variable LoadCustomConfig
// assigns so tests can restore them.
func saveCustomSettingVars(t *testing.T) {
	t.Helper()
	queueEnabled := ConcurrencyQueueEnabled
	queueScope := ConcurrencyQueueScope
	queueLimit := ConcurrencyQueueDefaultLimit
	queueTimeout := ConcurrencyQueueTimeoutSeconds
	ConcurrencyQueueMutex.RLock()
	queueGroupLimit := ConcurrencyQueueGroupLimit
	ConcurrencyQueueMutex.RUnlock()
	pollingEnabled := ChannelPollingEnabled
	pollingScope := ChannelPollingScope
	rules := operation_setting.ChannelErrorKeywordActions
	tokenOverride := DisableTokenGroupOverride
	patchChannels := PatchMissingThinkingChannels
	t.Cleanup(func() {
		ConcurrencyQueueEnabled = queueEnabled
		ConcurrencyQueueScope = queueScope
		ConcurrencyQueueDefaultLimit = queueLimit
		ConcurrencyQueueTimeoutSeconds = queueTimeout
		ConcurrencyQueueMutex.Lock()
		ConcurrencyQueueGroupLimit = queueGroupLimit
		ConcurrencyQueueMutex.Unlock()
		ChannelPollingEnabled = pollingEnabled
		ChannelPollingScope = pollingScope
		operation_setting.ChannelErrorKeywordActions = rules
		DisableTokenGroupOverride = tokenOverride
		PatchMissingThinkingChannels = patchChannels
	})
}

func writeCustomConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "custom-config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadCustomConfigMissingFileKeepsDefaults(t *testing.T) {
	saveCustomSettingVars(t)

	err := LoadCustomConfig(filepath.Join(t.TempDir(), "absent.yaml"))
	require.NoError(t, err)

	assert.False(t, ConcurrencyQueueEnabled)
	assert.Equal(t, ConcurrencyQueueScopeUser, ConcurrencyQueueScope)
	assert.Empty(t, PatchMissingThinkingChannels)
	assert.Equal(t, defaultChannelErrorRulesForTest, operation_setting.ChannelErrorKeywordActions)
}

func TestLoadCustomConfigValidAppliesAllSections(t *testing.T) {
	saveCustomSettingVars(t)
	path := writeCustomConfig(t, `
concurrency_queue:
  enabled: true
  scope: token
  default_limit: 5
  timeout_seconds: 10
  group_limits:
    default: 3
    vip: 0
channel_polling:
  enabled: true
  scope: token
channel_error_rules:
  rules:
    - keywords: ["boom"]
      action: disable
token_group_override:
  disable: true
claude_missing_thinking_patch:
  channel_ids: [30, 31]
`)

	require.NoError(t, LoadCustomConfig(path))

	assert.True(t, ConcurrencyQueueEnabled)
	assert.Equal(t, ConcurrencyQueueScopeToken, ConcurrencyQueueScope)
	assert.Equal(t, 5, ConcurrencyQueueDefaultLimit)
	assert.Equal(t, 10, ConcurrencyQueueTimeoutSeconds)
	ConcurrencyQueueMutex.RLock()
	assert.Equal(t, map[string]int{"default": 3, "vip": 0}, ConcurrencyQueueGroupLimit)
	ConcurrencyQueueMutex.RUnlock()

	assert.True(t, ChannelPollingEnabled)
	assert.Equal(t, ChannelPollingScopeToken, ChannelPollingScope)

	require.Len(t, operation_setting.ChannelErrorKeywordActions, 1)
	assert.Equal(t, []string{"boom"}, operation_setting.ChannelErrorKeywordActions[0].Keywords)
	assert.Equal(t, operation_setting.ChannelErrorActionDisable, operation_setting.ChannelErrorKeywordActions[0].Action)

	assert.True(t, DisableTokenGroupOverride)
	assert.Equal(t, map[int]struct{}{30: {}, 31: {}}, PatchMissingThinkingChannels)
}

func TestLoadCustomConfigEmptyRulesKeepDefaults(t *testing.T) {
	saveCustomSettingVars(t)
	path := writeCustomConfig(t, "concurrency_queue:\n  enabled: true\n")

	require.NoError(t, LoadCustomConfig(path))

	assert.True(t, ConcurrencyQueueEnabled)
	assert.Equal(t, defaultChannelErrorRulesForTest, operation_setting.ChannelErrorKeywordActions)
}

func TestLoadCustomConfigOptionalFieldsKeepBuiltIns(t *testing.T) {
	saveCustomSettingVars(t)
	path := writeCustomConfig(t, "concurrency_queue:\n  enabled: true\n")

	require.NoError(t, LoadCustomConfig(path))

	assert.Equal(t, ConcurrencyQueueScopeUser, ConcurrencyQueueScope)
	assert.Equal(t, 2, ConcurrencyQueueDefaultLimit)
	assert.Equal(t, 30, ConcurrencyQueueTimeoutSeconds)
}

func TestLoadCustomConfigExplicitZeroLimitMeansUnlimited(t *testing.T) {
	saveCustomSettingVars(t)
	path := writeCustomConfig(t, "concurrency_queue:\n  enabled: true\n  default_limit: 0\n")

	require.NoError(t, LoadCustomConfig(path))

	// 显式 0 是合法值（不限），不得与"未写"（保持默认 2）混淆
	assert.Equal(t, 0, ConcurrencyQueueDefaultLimit)
}

func TestLoadCustomConfigRejectsInvalid(t *testing.T) {
	saveCustomSettingVars(t)
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"malformed yaml", "concurrency_queue: [broken\n", "parse"},
		{"bad queue scope", "concurrency_queue:\n  scope: tenant\n", "scope"},
		{"negative default limit", "concurrency_queue:\n  default_limit: -1\n", "default_limit"},
		{"negative timeout", "concurrency_queue:\n  timeout_seconds: -5\n", "timeout_seconds"},
		{"negative group limit", "concurrency_queue:\n  group_limits:\n    bad: -1\n", "group_limits"},
		{"bad polling scope", "channel_polling:\n  scope: tenant\n", "scope"},
		{"empty keywords", "channel_error_rules:\n  rules:\n    - action: disable\n", "no keywords"},
		{"invalid action", "channel_error_rules:\n  rules:\n    - keywords: [\"x\"]\n      action: explode\n", "invalid action"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := LoadCustomConfig(writeCustomConfig(t, tc.content))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestDefaultCustomConfigPath(t *testing.T) {
	t.Setenv("CUSTOM_CONFIG_PATH", "")
	assert.Equal(t, "custom-config.yaml", DefaultCustomConfigPath())
	t.Setenv("CUSTOM_CONFIG_PATH", "/etc/new-api/custom.yaml")
	assert.Equal(t, "/etc/new-api/custom.yaml", DefaultCustomConfigPath())
}
