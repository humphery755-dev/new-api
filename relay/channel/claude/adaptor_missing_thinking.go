package claude

import (
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// patchMissingThinkingBlock repairs broken thinking chains for strict
// Anthropic-compatible upstreams (DeepSeek's /anthropic endpoint, GLM):
// with thinking enabled they answer 400 "The `content[].thinking` in the
// thinking mode must be passed back to the API" when a tool_use turn carries
// no thinking block and the request ends with a tool_result. A conversation
// load-balanced across upstreams breaks this way when another upstream
// returned tool_use turns without thinking (for example MiniMax under
// adaptive thinking), so every such turn in the history must be repaired, not
// just the last one. The request is patched per channel, gated by
// claude_missing_thinking_patch.channel_ids in custom-config.yaml. The
// inserted placeholder carries no signature, which official Anthropic
// upstreams reject, so the patch must only list channels that tolerate it.
func patchMissingThinkingBlock(request *dto.ClaudeRequest) {
	if request.Thinking == nil ||
		(request.Thinking.Type != "enabled" && request.Thinking.Type != "adaptive") {
		return
	}
	if len(request.Messages) == 0 {
		return
	}
	// The upstream validation only fires when the conversation continues
	// right after a tool result, so the request must end with one.
	if !contentHasToolResultBlock(request.Messages[len(request.Messages)-1].Content) {
		return
	}
	// A non-empty placeholder is required: Pydantic-style Anthropic gateways
	// (for example the TYDIC-LLM platform behind channel tydic-glm) treat an
	// empty thinking string as a missing field and answer 400 "thinking:
	// Field required". DeepSeek and MiniMax accept both, so use ".".
	placeholder := map[string]any{"type": "thinking", "thinking": "."}
	for i := range request.Messages {
		if request.Messages[i].Role != "assistant" {
			continue
		}
		content := request.Messages[i].Content
		if contentHasThinkingBlock(content) || !contentHasToolUseBlock(content) {
			continue
		}
		request.Messages[i].Content = append([]any{placeholder}, content.([]any)...)
	}
}

func contentHasThinkingBlock(content any) bool {
	blocks, ok := content.([]any)
	if !ok {
		return false
	}
	for _, block := range blocks {
		blockMap, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := blockMap["type"].(string); t == "thinking" || t == "redacted_thinking" {
			return true
		}
	}
	return false
}

func contentHasToolResultBlock(content any) bool {
	blocks, ok := content.([]any)
	if !ok {
		return false
	}
	for _, block := range blocks {
		blockMap, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := blockMap["type"].(string); t == "tool_result" {
			return true
		}
	}
	return false
}

func contentHasToolUseBlock(content any) bool {
	blocks, ok := content.([]any)
	if !ok {
		return false
	}
	for _, block := range blocks {
		blockMap, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := blockMap["type"].(string); t == "tool_use" {
			return true
		}
	}
	return false
}
