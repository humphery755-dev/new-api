package claude

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertClaudeRequestTreatsZeroMaxTokensAsUnset(t *testing.T) {
	zero := uint(0)
	req := &dto.ClaudeRequest{
		Model:     "claude-sonnet-4-5",
		MaxTokens: &zero,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-sonnet-4-5",
		},
	}

	out, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, req)
	require.NoError(t, err)
	converted, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok)
	require.NotNil(t, converted.MaxTokens)
	assert.Equal(t, uint(model_setting.GetClaudeSettings().GetDefaultMaxTokens(req.Model)), *converted.MaxTokens)
}

func TestConvertClaudeRequestPreservesNativeClaudeCodeThinking(t *testing.T) {
	budget := 10000
	maxTokens := uint(20000)
	temperature := 0.7
	topP := 0.9
	req := &dto.ClaudeRequest{
		Model:        "claude-opus-4-8",
		MaxTokens:    &maxTokens,
		Temperature:  &temperature,
		TopP:         &topP,
		Thinking:     &dto.Thinking{Type: "enabled", BudgetTokens: &budget},
		OutputConfig: []byte(`{"effort":"high"}`),
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: req.Model,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: req.Model,
		},
	}

	out, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, req)
	require.NoError(t, err)
	converted, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok)
	require.NotNil(t, converted.Thinking)
	assert.Equal(t, "enabled", converted.Thinking.Type)
	require.NotNil(t, converted.Thinking.BudgetTokens)
	assert.Equal(t, budget, *converted.Thinking.BudgetTokens)
	assert.Equal(t, temperature, *converted.Temperature)
	assert.Equal(t, topP, *converted.TopP)
	assert.JSONEq(t, `{"effort":"high"}`, string(converted.OutputConfig))
	assert.Empty(t, info.ConversionDiagnostics())
}

func TestConvertClaudeRequestPreservesMessageOutputConfig(t *testing.T) {
	body := `{"model":"claude-opus-5-5","max_tokens":64,"output_config":{"effort":"medium"},"messages":[` +
		`{"role":"user","content":"summary"},` +
		`{"role":"system","content":[],"output_config":{"effort":"high"}},` +
		`{"role":"assistant","content":"done"}]}`
	var req dto.ClaudeRequest
	require.NoError(t, common.UnmarshalJsonStr(body, &req))
	info := &relaycommon.RelayInfo{
		OriginModelName: req.Model,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: req.Model,
		},
	}

	out, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, &req)
	require.NoError(t, err)
	encoded, err := common.Marshal(out)
	require.NoError(t, err)

	var upstream struct {
		Messages []map[string]any `json:"messages"`
	}
	require.NoError(t, common.Unmarshal(encoded, &upstream))
	require.Len(t, upstream.Messages, 3)
	assert.Equal(t, map[string]any{"effort": "high"}, upstream.Messages[1]["output_config"])
	assert.NotContains(t, upstream.Messages[0], "output_config")
	assert.NotContains(t, upstream.Messages[2], "output_config")
}

func TestConvertClaudeRequestZeroMaxTokensStillRaisesThinkingBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	zero := uint(0)
	original := &dto.ClaudeRequest{
		Model:     "claude-3-7-sonnet-thinking",
		MaxTokens: &zero,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet-thinking",
		Request:         original,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-3-7-sonnet-thinking",
		},
	}
	outbound, err := common.DeepCopy(original)
	require.NoError(t, err)
	require.NoError(t, helper.ModelMappedHelper(c, info, outbound))
	err = helper.ApplyReasoningModelSuffix(nil, info, outbound)
	require.NoError(t, err)

	out, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, outbound)
	require.NoError(t, err)
	converted, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok)
	assert.Equal(t, "claude-3-7-sonnet", converted.Model)
	require.NotNil(t, converted.Thinking)
	require.NotNil(t, converted.MaxTokens)
	assert.Greater(t, *converted.MaxTokens, uint(1024))
}

func TestConvertClaudeRequestDoesNotOverwriteTrimmedUpstreamModelName(t *testing.T) {
	req := &dto.ClaudeRequest{
		Model: "claude-3-7-sonnet-thinking",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-3-7-sonnet",
		},
	}

	_, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, req)
	require.NoError(t, err)
	assert.Equal(t, "claude-3-7-sonnet", info.UpstreamModelName)
}

func TestConvertClaudeRequestForwardsThinkingBlockBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name         string
		model        string
		thinking     string
		wantThinking string
	}{
		{
			name:         "native request keeps every thinking field",
			model:        "claude-opus-5-5",
			thinking:     `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"},"future_field":{"a":1}}`,
			wantThinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"},"future_field":{"a":1}}`,
		},
		{
			name:         "effort suffix re-renders adaptive and keeps block_binding",
			model:        "claude-opus-5-5-high",
			thinking:     `{"type":"adaptive","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`,
			wantThinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`,
		},
		{
			name:         "suffix rendering between_tools does not carry block_binding",
			model:        "claude-sonnet-5-5-none",
			thinking:     `{"type":"adaptive","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`,
			wantThinking: `{"type":"between_tools"}`,
		},
		{
			name:         "request without block_binding gets none",
			model:        "claude-opus-5-5-high",
			thinking:     `{"type":"adaptive"}`,
			wantThinking: `{"type":"adaptive","display":"summarized"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := `{"model":"` + tt.model + `","max_tokens":1024,"thinking":` + tt.thinking +
				`,"messages":[{"role":"user","content":"hi"}]}`
			var original dto.ClaudeRequest
			require.NoError(t, common.UnmarshalJsonStr(body, &original))
			info := &relaycommon.RelayInfo{
				OriginModelName: tt.model,
				Request:         &original,
				ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: tt.model},
			}
			// Same steps as the Claude handler before the adaptor converts.
			outbound, err := common.DeepCopy(&original)
			require.NoError(t, err)
			require.NoError(t, helper.ModelMappedHelper(c, info, outbound))
			require.NoError(t, helper.ApplyReasoningModelSuffix(nil, info, outbound))

			out, err := (&Adaptor{}).ConvertClaudeRequest(c, info, outbound)
			require.NoError(t, err)
			encoded, err := common.Marshal(out)
			require.NoError(t, err)
			var upstream struct {
				Thinking json.RawMessage `json:"thinking"`
			}
			require.NoError(t, common.Unmarshal(encoded, &upstream))
			assert.JSONEq(t, tt.wantThinking, string(upstream.Thinking))
		})
	}
}

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

func TestSetupRequestHeaderForwardsEveryAnthropicBetaValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Add("anthropic-beta", "fast-mode-2026-02-01")
	c.Request.Header.Add("anthropic-beta", "thinking-binding-controls-2026-08-01")
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-opus-5-5",
		ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "sk-test"},
	}

	header := http.Header{}
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &header, info))
	assert.Equal(t, []string{"fast-mode-2026-02-01,thinking-binding-controls-2026-08-01"}, header.Values("anthropic-beta"))
}

func geminiToClaudeInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-3-7-sonnet",
		},
	}
}

func TestConvertGeminiRequestMapsSystemInstructionToolsAndMultimodal(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{
				Role: "user",
				Parts: []dto.GeminiPart{
					{Text: "What is in this image?"},
					{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "aGVsbG8="}},
				},
			},
		},
		SystemInstructions: &dto.GeminiChatContent{
			Parts: []dto.GeminiPart{{Text: "You are a helpful assistant."}},
		},
	}
	req.SetTools([]dto.GeminiChatTool{
		{
			FunctionDeclarations: []dto.FunctionRequest{
				{
					Name:        "lookup",
					Description: "Lookup data",
					Parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
				},
			},
		},
	})

	out, err := (&Adaptor{}).ConvertGeminiRequest(nil, geminiToClaudeInfo(), req)
	require.NoError(t, err)
	converted, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok)

	system := converted.ParseSystem()
	require.NotEmpty(t, system)
	assert.Contains(t, system[0].GetText(), "You are a helpful assistant.")
	require.NotEmpty(t, converted.Messages)
	assert.Equal(t, "user", converted.Messages[0].Role)

	blocks, parseErr := converted.Messages[0].ParseContent()
	require.NoError(t, parseErr)
	var foundImage bool
	for _, block := range blocks {
		if block.Type == "image" || (block.Source != nil && block.Source.Type == "base64") {
			foundImage = true
			break
		}
	}
	assert.True(t, foundImage)

	require.NotNil(t, converted.Tools)
	tools, err := common.Marshal(converted.Tools)
	require.NoError(t, err)
	assert.Contains(t, string(tools), `"lookup"`)
	require.NotNil(t, converted.MaxTokens)
	assert.Greater(t, *converted.MaxTokens, uint(0))
}

func TestConvertGeminiRequestThinkingConfigUsesReasoningIntent(t *testing.T) {
	budget := 1024
	maxTokens := uint(4096)
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "think"}}},
		},
		GenerationConfig: dto.GeminiChatGenerationConfig{
			MaxOutputTokens: &maxTokens,
			ThinkingConfig:  &dto.GeminiThinkingConfig{ThinkingBudget: &budget},
		},
	}

	out, err := (&Adaptor{}).ConvertGeminiRequest(nil, geminiToClaudeInfo(), req)
	require.NoError(t, err)
	converted, ok := out.(*dto.ClaudeRequest)
	require.True(t, ok)
	require.NotNil(t, converted.Thinking)
	assert.Equal(t, "enabled", converted.Thinking.Type)
	require.NotNil(t, converted.Thinking.BudgetTokens)
	assert.Equal(t, 1024, *converted.Thinking.BudgetTokens)
}

func TestConvertGeminiRequestNilRequest(t *testing.T) {
	_, err := (&Adaptor{}).ConvertGeminiRequest(nil, geminiToClaudeInfo(), nil)
	require.Error(t, err)
}
