package claude

import (
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// patchServerToolUseHistory rewrites assistant turns that carry server-side
// tool blocks into the standard tool_use/tool_result pairing that strict
// Anthropic-compatible upstreams validate.
//
// GLM's Anthropic-compatible endpoint answers a tool call with a
// server_tool_use block and returns the server-side result as a tool_result
// block inside the same assistant message. Claude Code replays that history
// verbatim, and an upstream that validates tool ids rejects it (MiniMax
// answers 400 "tool result's tool id(...) not found (2013)"): it registers
// tool ids only from tool_use blocks, and none at all from an assistant
// message that embeds a tool_result. The repair renames server_tool_use to
// tool_use and moves the embedded tool_result blocks into the following user
// message, which every strict upstream accepts. Applied per channel, gated by
// claude_server_tool_use_patch.channel_ids in custom-config.yaml.
func patchServerToolUseHistory(request *dto.ClaudeRequest) {
	if request == nil || len(request.Messages) == 0 {
		return
	}
	messages := make([]dto.ClaudeMessage, 0, len(request.Messages))
	for i := 0; i < len(request.Messages); i++ {
		message := request.Messages[i]
		if message.Role != "assistant" {
			messages = append(messages, message)
			continue
		}
		rewritten, leftovers := rewriteAssistantTurn(message)
		if len(leftovers) > 0 {
			if len(rewritten) == 0 {
				// The assistant message carried nothing but tool results:
				// drop the empty shell and hand them to the next user turn.
				if i+1 < len(request.Messages) && request.Messages[i+1].Role == "user" {
					next := request.Messages[i+1]
					next.Content = prependToolResultBlocks(next.Content, leftovers)
					messages = append(messages, next)
					i++
					continue
				}
				messages = append(messages, dto.ClaudeMessage{Role: "user", Content: leftovers})
				continue
			}
			if i+1 < len(request.Messages) && request.Messages[i+1].Role == "user" {
				next := request.Messages[i+1]
				next.Content = prependToolResultBlocks(next.Content, leftovers)
				messages = append(messages, rewritten...)
				messages = append(messages, next)
				i++
				continue
			}
			rewritten = append(rewritten, dto.ClaudeMessage{Role: "user", Content: leftovers})
		}
		// A split can leave two assistant turns adjacent; merge the next
		// assistant message into the last rewritten one to keep roles
		// alternating.
		if i+1 < len(request.Messages) && request.Messages[i+1].Role == "assistant" && len(rewritten) > 0 {
			last := &rewritten[len(rewritten)-1]
			if last.Role == "assistant" {
				next := request.Messages[i+1]
				last.Content = mergeAssistantContent(last.Content, next.Content)
				if len(last.OutputConfig) == 0 {
					last.OutputConfig = next.OutputConfig
				}
				i++
			}
		}
		messages = append(messages, rewritten...)
	}
	request.Messages = messages
}

// rewriteAssistantTurn canonicalizes one assistant turn into messages where
// every tool_use block ends its own assistant message and is followed by a
// user message carrying its tool_result. It returns the rewritten messages
// and any extracted tool_result blocks it could not match to a tool_use in
// the same turn (the caller places them after the turn).
func rewriteAssistantTurn(message dto.ClaudeMessage) ([]dto.ClaudeMessage, []any) {
	blocks, ok := message.Content.([]any)
	if !ok {
		return []dto.ClaudeMessage{message}, nil
	}
	var extracted []any
	cleaned := make([]any, 0, len(blocks))
	for _, block := range blocks {
		blockMap, ok := block.(map[string]any)
		if !ok {
			cleaned = append(cleaned, block)
			continue
		}
		switch blockType, _ := blockMap["type"].(string); blockType {
		case "server_tool_use":
			blockMap["type"] = "tool_use"
			cleaned = append(cleaned, blockMap)
		case "tool_result":
			extracted = append(extracted, blockMap)
		default:
			cleaned = append(cleaned, blockMap)
		}
	}
	if len(extracted) == 0 {
		return []dto.ClaudeMessage{message}, nil
	}
	// Segment the cleaned blocks so every segment ends with its tool_use.
	var segments [][]any
	current := make([]any, 0, len(cleaned))
	for _, block := range cleaned {
		current = append(current, block)
		if blockMap, ok := block.(map[string]any); ok {
			if blockType, _ := blockMap["type"].(string); blockType == "tool_use" {
				segments = append(segments, current)
				current = nil
			}
		}
	}
	if len(current) > 0 {
		segments = append(segments, current)
	}
	if len(segments) == 0 {
		return nil, extracted
	}
	rewritten := make([]dto.ClaudeMessage, 0, len(segments)*2)
	for _, segment := range segments {
		rewritten = append(rewritten, dto.ClaudeMessage{Role: "assistant", Content: segment})
		last := segment[len(segment)-1]
		lastMap, ok := last.(map[string]any)
		if !ok {
			continue
		}
		if blockType, _ := lastMap["type"].(string); blockType != "tool_use" {
			continue
		}
		toolUseID, _ := lastMap["id"].(string)
		for idx, result := range extracted {
			resultMap, ok := result.(map[string]any)
			if !ok {
				continue
			}
			if resultID, _ := resultMap["tool_use_id"].(string); resultID == toolUseID {
				rewritten = append(rewritten, dto.ClaudeMessage{Role: "user", Content: []any{result}})
				extracted = append(extracted[:idx], extracted[idx+1:]...)
				break
			}
		}
	}
	return rewritten, extracted
}

// prependToolResultBlocks puts the extracted tool_result blocks in front of
// the next user turn's content, converting string content into a text block.
func prependToolResultBlocks(content any, blocks []any) any {
	switch content := content.(type) {
	case []any:
		return append(blocks, content...)
	case string:
		return append(blocks, map[string]any{"type": "text", "text": content})
	default:
		return blocks
	}
}

// mergeAssistantContent joins two consecutive assistant turns' content.
func mergeAssistantContent(first, second any) any {
	switch second := second.(type) {
	case []any:
		if firstBlocks, ok := first.([]any); ok {
			return append(firstBlocks, second...)
		}
	case string:
		if firstBlocks, ok := first.([]any); ok {
			return append(firstBlocks, map[string]any{"type": "text", "text": second})
		}
	}
	return first
}
