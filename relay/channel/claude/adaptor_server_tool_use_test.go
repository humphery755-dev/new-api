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

func TestConvertClaudeRequestRewritesServerToolUseHistory(t *testing.T) {
	// GLM 编码端点实测形状：assistant 回合内联 server_tool_use + 服务端
	// tool_result，且同一回合还带普通 tool_use（其后 user 消息带对应结果）。
	glmTurn := `{"role":"assistant","content":[{"type":"thinking","thinking":"看图","signature":"sig"},{"type":"text","text":"我调用分析工具"},{"type":"server_tool_use","id":"call_A","name":"analyze_image","input":{}},{"type":"text","text":"分析完成"},{"type":"tool_result","tool_use_id":"call_A","content":"报告内容"},{"type":"thinking","thinking":"继续","signature":"sig"},{"type":"tool_use","id":"call_B","name":"Read","input":{"file_path":"/tmp/x.png"}}]}`
	// 无尾随 tool_use 的 GLM 形状。
	glmTurnNoTrailing := `{"role":"assistant","content":[{"type":"thinking","thinking":"看图","signature":"sig"},{"type":"text","text":"我调用分析工具"},{"type":"server_tool_use","id":"call_A","name":"analyze_image","input":{}},{"type":"text","text":"分析完成"},{"type":"tool_result","tool_use_id":"call_A","content":"报告内容"}]}`
	normalToolResult := `{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_B","content":"图像"}]}`
	cleanHistory := `[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01A","name":"get_weather","input":{"city":"SF"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01A","content":"sunny"}]}]`

	tests := []struct {
		name          string
		channelListed bool
		messages      string
		want          string
	}{
		{
			name:          "channel not listed leaves GLM-shaped history untouched",
			channelListed: false,
			messages:      `[{"role":"user","content":"hi"},` + glmTurn + `,` + normalToolResult + `]`,
			want:          `[{"role":"user","content":"hi"},` + glmTurn + `,` + normalToolResult + `]`,
		},
		{
			name:          "server tool turn splits into per-tool-use pairings",
			channelListed: true,
			messages:      `[{"role":"user","content":"hi"},` + glmTurn + `,` + normalToolResult + `]`,
			want: `[{"role":"user","content":"hi"},` +
				`{"role":"assistant","content":[{"type":"thinking","thinking":"看图","signature":"sig"},{"type":"text","text":"我调用分析工具"},{"type":"tool_use","id":"call_A","name":"analyze_image","input":{}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_A","content":"报告内容"}]},` +
				`{"role":"assistant","content":[{"type":"text","text":"分析完成"},{"type":"thinking","thinking":"继续","signature":"sig"},{"type":"tool_use","id":"call_B","name":"Read","input":{"file_path":"/tmp/x.png"}}]},` +
				normalToolResult + `]`,
		},
		{
			name:          "server tool turn without trailing tool call keeps the following string user turn",
			channelListed: true,
			messages:      `[{"role":"user","content":"hi"},` + glmTurnNoTrailing + `,{"role":"user","content":"继续"}]`,
			want: `[{"role":"user","content":"hi"},` +
				`{"role":"assistant","content":[{"type":"thinking","thinking":"看图","signature":"sig"},{"type":"text","text":"我调用分析工具"},{"type":"tool_use","id":"call_A","name":"analyze_image","input":{}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_A","content":"报告内容"}]},` +
				`{"role":"assistant","content":[{"type":"text","text":"分析完成"}]},` +
				`{"role":"user","content":"继续"}]`,
		},
		{
			name:          "following assistant turn merges into the trailing segment",
			channelListed: true,
			messages:      `[{"role":"user","content":"hi"},` + glmTurnNoTrailing + `,{"role":"assistant","content":[{"type":"text","text":"done"}]}]`,
			want: `[{"role":"user","content":"hi"},` +
				`{"role":"assistant","content":[{"type":"thinking","thinking":"看图","signature":"sig"},{"type":"text","text":"我调用分析工具"},{"type":"tool_use","id":"call_A","name":"analyze_image","input":{}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_A","content":"报告内容"}]},` +
				`{"role":"assistant","content":[{"type":"text","text":"分析完成"},{"type":"text","text":"done"}]}]`,
		},
		{
			name:          "history without server blocks untouched",
			channelListed: true,
			messages:      cleanHistory,
			want:          cleanHistory,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting.PatchServerToolUseChannels = map[int]struct{}{}
			if tt.channelListed {
				setting.PatchServerToolUseChannels[5] = struct{}{}
			}
			t.Cleanup(func() { setting.PatchServerToolUseChannels = map[int]struct{}{} })
			body := `{"model":"claude-opus-4-8","max_tokens":2048,"messages":` + tt.messages + `}`
			var req dto.ClaudeRequest
			require.NoError(t, common.UnmarshalJsonStr(body, &req))
			info := &relaycommon.RelayInfo{
				OriginModelName: "claude-opus-4-8",
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: "claude-opus-4-8",
					ChannelId:         5,
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
