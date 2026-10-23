package claude

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertClaudeRequestPatchesMissingThinking(t *testing.T) {
	thinkingEnabled := `"thinking":{"type":"enabled","budget_tokens":1024}`
	thinkingDisabled := `"thinking":{"type":"disabled"}`
	thinkingTurn := `{"role":"assistant","content":[{"type":"thinking","thinking":"call the tool","signature":"sig"},{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{"city":"SF"}}]}`
	toolResult := `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01A","content":"sunny"}]}`

	tests := []struct {
		name          string
		channelListed bool
		thinking      string
		messages      string
		want          string
	}{
		{
			name:          "channel not listed leaves broken chain untouched",
			channelListed: false,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,{"role":"assistant","content":[{"type":"text","text":"done"}]}]`,
			want:          `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,{"role":"assistant","content":[{"type":"text","text":"done"}]}]`,
		},
		{
			name:          "no thinking history untouched",
			channelListed: true,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"done"}]}]`,
			want:          `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"done"}]}]`,
		},
		{
			name:          "last assistant already has thinking untouched",
			channelListed: true,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,` + thinkingTurn + `,` + toolResult + `]`,
			want:          `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,` + thinkingTurn + `,` + toolResult + `]`,
		},
		{
			name:          "broken chain gets placeholder thinking block",
			channelListed: true,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,{"role":"assistant","content":[{"type":"text","text":"done"},{"type":"tool_use","id":"toolu_01B","name":"get_weather","input":{"city":"LA"}}]}]`,
			want:          `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,{"role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"text","text":"done"},{"type":"tool_use","id":"toolu_01B","name":"get_weather","input":{"city":"LA"}}]}]`,
		},
		{
			name:          "string content becomes thinking plus text blocks",
			channelListed: true,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,{"role":"assistant","content":"done"}]`,
			want:          `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,{"role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"text","text":"done"}]}]`,
		},
		{
			name:          "redacted thinking counts as thinking history",
			channelListed: true,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"ok"}]},{"role":"user","content":"again"},{"role":"assistant","content":[{"type":"text","text":"done"}]}]`,
			want:          `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"ok"}]},{"role":"user","content":"again"},{"role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"text","text":"done"}]}]`,
		},
		{
			name:          "thinking disabled untouched",
			channelListed: true,
			thinking:      thinkingDisabled,
			messages:      `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,{"role":"assistant","content":[{"type":"text","text":"done"}]}]`,
			want:          `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,{"role":"assistant","content":[{"type":"text","text":"done"}]}]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting.PatchMissingThinkingChannels = map[int]struct{}{}
			if tt.channelListed {
				setting.PatchMissingThinkingChannels[30] = struct{}{}
			}
			t.Cleanup(func() { setting.PatchMissingThinkingChannels = map[int]struct{}{} })
			body := `{"model":"claude-opus-4-8","max_tokens":2048,` + tt.thinking + `,"messages":` + tt.messages + `}`
			var req dto.ClaudeRequest
			require.NoError(t, common.UnmarshalJsonStr(body, &req))
			info := &relaycommon.RelayInfo{
				OriginModelName: "claude-opus-4-8",
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: "claude-opus-4-8",
					ChannelId:         30,
				},
			}

			out, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, &req)
			require.NoError(t, err)
			encoded, err := common.Marshal(out)
			require.NoError(t, err)
			var upstream struct {
				Messages json.RawMessage `json:"messages"`
			}
			require.NoError(t, common.Unmarshal(encoded, &upstream))
			assert.JSONEq(t, tt.want, string(upstream.Messages))
		})
	}
}
