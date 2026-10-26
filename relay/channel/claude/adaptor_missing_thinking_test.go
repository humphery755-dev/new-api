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
	thinkingAdaptive := `"thinking":{"type":"adaptive"}`
	thinkingDisabled := `"thinking":{"type":"disabled"}`
	thinkingTurn := `{"role":"assistant","content":[{"type":"thinking","thinking":"call the tool","signature":"sig"},{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{"city":"SF"}}]}`
	toolUseTurn := `{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{"city":"SF"}}]}`
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
			messages:      `[{"role":"user","content":"hi"},` + toolUseTurn + `,` + toolResult + `]`,
			want:          `[{"role":"user","content":"hi"},` + toolUseTurn + `,` + toolResult + `]`,
		},
		{
			name:          "thinking disabled untouched",
			channelListed: true,
			thinking:      thinkingDisabled,
			messages:      `[{"role":"user","content":"hi"},` + toolUseTurn + `,` + toolResult + `]`,
			want:          `[{"role":"user","content":"hi"},` + toolUseTurn + `,` + toolResult + `]`,
		},
		{
			name:          "request not ending with a tool result untouched",
			channelListed: true,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,{"role":"assistant","content":[{"type":"text","text":"done"},{"type":"tool_use","id":"toolu_01B","name":"get_weather","input":{"city":"LA"}}]}]`,
			want:          `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `,{"role":"assistant","content":[{"type":"text","text":"done"},{"type":"tool_use","id":"toolu_01B","name":"get_weather","input":{"city":"LA"}}]}]`,
		},
		{
			name:          "tool call turn already has thinking untouched",
			channelListed: true,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `]`,
			want:          `[{"role":"user","content":"hi"},` + thinkingTurn + `,` + toolResult + `]`,
		},
		{
			name:          "tool call turn without thinking gets placeholder",
			channelListed: true,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},` + toolUseTurn + `,` + toolResult + `]`,
			want:          `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{"city":"SF"}}]},` + toolResult + `]`,
		},
		{
			name:          "adaptive thinking gets placeholder",
			channelListed: true,
			thinking:      thinkingAdaptive,
			messages:      `[{"role":"user","content":"hi"},` + toolUseTurn + `,` + toolResult + `]`,
			want:          `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{"city":"SF"}}]},` + toolResult + `]`,
		},
		{
			name:          "earlier thinking history does not change the repair",
			channelListed: true,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":"earlier","signature":"sig"},{"type":"text","text":"ok"}]},{"role":"user","content":"again"},` + toolUseTurn + `,` + toolResult + `]`,
			want:          `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":"earlier","signature":"sig"},{"type":"text","text":"ok"}]},{"role":"user","content":"again"},{"role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{"city":"SF"}}]},` + toolResult + `]`,
		},
		{
			name:          "tool result without a tool call turn untouched",
			channelListed: true,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"done"}]},` + toolResult + `]`,
			want:          `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"done"}]},` + toolResult + `]`,
		},
		{
			name:          "every tool call turn without thinking is repaired",
			channelListed: true,
			thinking:      thinkingEnabled,
			messages:      `[{"role":"user","content":"hi"},` + toolUseTurn + `,` + toolResult + `,{"role":"user","content":"again"},` + thinkingTurn + `,` + toolResult + `,{"role":"user","content":"more"},` + toolUseTurn + `,` + toolResult + `]`,
			want:          `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{"city":"SF"}}]},` + toolResult + `,{"role":"user","content":"again"},` + thinkingTurn + `,` + toolResult + `,{"role":"user","content":"more"},{"role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{"city":"SF"}}]},` + toolResult + `]`,
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
