package claude

import (
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// patchMissingThinkingBlock repairs a broken thinking chain for upstreams
// that keep validating thinking passthrough once the conversation has produced
// thinking blocks (GLM's Anthropic-compatible endpoint answers 400 "The
// `content[].thinking` in the thinking mode must be passed back to the API"
// when the last assistant message lacks a thinking block). Only the last
// assistant message is checked, matching the upstream validation. The inserted
// placeholder carries no signature, which official Anthropic upstreams reject,
// so the patch is gated by an explicit channel allowlist.
func patchMissingThinkingBlock(request *dto.ClaudeRequest) {
	if request.Thinking == nil || request.Thinking.Type != "enabled" {
		return
	}
	lastAssistant := -1
	lastHasThinking := false
	sawThinking := false
	for i := range request.Messages {
		if request.Messages[i].Role != "assistant" {
			continue
		}
		lastAssistant = i
		lastHasThinking = contentHasThinkingBlock(request.Messages[i].Content)
		if lastHasThinking {
			sawThinking = true
		}
	}
	if !sawThinking || lastAssistant < 0 || lastHasThinking {
		return
	}
	placeholder := map[string]any{"type": "thinking", "thinking": ""}
	switch content := request.Messages[lastAssistant].Content.(type) {
	case []any:
		request.Messages[lastAssistant].Content = append([]any{placeholder}, content...)
	case string:
		request.Messages[lastAssistant].Content = []any{placeholder, map[string]any{"type": "text", "text": content}}
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
