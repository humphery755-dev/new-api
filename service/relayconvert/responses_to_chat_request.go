package openaicompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/samber/lo"
)

func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}

	messages, err := responsesInputToChatMessages(req.Input, req.Instructions)
	if err != nil {
		return nil, fmt.Errorf("failed to convert input to messages: %w", err)
	}

	out := &dto.GeneralOpenAIRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   req.Stream,
	}

	if req.Temperature != nil {
		out.Temperature = req.Temperature
	}
	if req.TopP != nil {
		out.TopP = req.TopP
	}
	if req.MaxOutputTokens != nil {
		out.MaxTokens = req.MaxOutputTokens
	}

	if len(req.Tools) > 0 {
		var toolsMap []map[string]any
		if err := common.Unmarshal(req.Tools, &toolsMap); err == nil {
			out.Tools = responsesToolsToChatTools(toolsMap)
		}
	}

	if len(req.ToolChoice) > 0 {
		out.ToolChoice = responsesToolChoiceToChatToolChoice(req.ToolChoice)
	}

	// We do NOT map reasoning.effort → thinking parameter.
	// Reasoning passthrough is handled in responsesInputToChatMessages:
	// reasoning items are associated with following function_calls (not assistant text
	// messages), matching va-ai-api-bridge's pending_tool_calls accumulation pattern.

	if lo.FromPtrOr(req.Stream, false) {
		out.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
	}

	return out, nil
}

func responsesInputToChatMessages(inputRaw json.RawMessage, instructionsRaw json.RawMessage) ([]dto.Message, error) {
	var messages []dto.Message

	if len(instructionsRaw) > 0 {
		var instructions string
		if err := common.Unmarshal(instructionsRaw, &instructions); err == nil && strings.TrimSpace(instructions) != "" {
			messages = append(messages, dto.Message{
				Role:    "system",
				Content: instructions,
			})
		}
	}

	if len(inputRaw) == 0 {
		return messages, nil
	}

	var inputStr string
	if err := common.Unmarshal(inputRaw, &inputStr); err == nil {
		if strings.TrimSpace(inputStr) != "" {
			messages = append(messages, dto.Message{
				Role:    "user",
				Content: inputStr,
			})
		}
		return messages, nil
	}

	var inputItems []map[string]any
	if err := common.Unmarshal(inputRaw, &inputItems); err != nil {
		return messages, nil
	}

	// Accumulators — mirrors va-ai-api-bridge's pending_tool_calls / pending_tool_content pattern.
	// Consecutive function_call items and any assistant message between them are merged
	// into a single assistant message so DeepSeek's tool_calls→tool message validation passes.
	var pendingToolCalls []dto.ToolCallRequest
	var pendingToolContent []string
	var pendingReasoning string

	flushToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		msg := dto.Message{
			Role:    "assistant",
			Content: strings.Join(pendingToolContent, ""),
		}
		if pendingReasoning != "" {
			rc := pendingReasoning; msg.ReasoningContent = &rc
		}
		msg.SetToolCalls(pendingToolCalls)
		messages = append(messages, msg)

		pendingToolCalls = nil
		pendingToolContent = nil
		pendingReasoning = ""
	}

	for _, item := range inputItems {
		itemType, _ := item["type"].(string)
		role, _ := item["role"].(string)

		// reasoning items — associate with following tool_calls (not the assistant text message).
		// DeepSeek thinking mode requires reasoning_content on the tool_calls assistant message.
		if itemType == "reasoning" {
			pendingReasoning = extractReasoningFromItem(item)
			continue
		}

		if itemType == "message" || role != "" {
			chatRole := role
			if chatRole == "" {
				chatRole = "user"
			}
			if chatRole == "developer" {
				chatRole = "system"
			}

			content := extractContentText(item["content"])

			// Assistant message between pending tool_calls → accumulate into tool content
			// (va-ai-api-bridge merges it into the final assistant tool_calls message)
			if chatRole == "assistant" && len(pendingToolCalls) > 0 {
				if content != "" {
					pendingToolContent = append(pendingToolContent, content)
				}
				// capture reasoning from this message if not already set from a reasoning item
				if pendingReasoning == "" {
					if rc := extractReasoningFromItem(item); rc != "" {
						pendingReasoning = rc
					}
				}
				continue
			}

			if chatRole == "assistant" && strings.TrimSpace(content) == "" {
				continue
			}

			flushToolCalls()

			// Do NOT attach pendingReasoning to standalone assistant messages.
			// Reasoning is only placed on the tool-calls assistant message via flushToolCalls,
			// matching va-ai-api-bridge where reasoning lives on ToolCall extensions.
			messages = append(messages, dto.Message{
				Role:    chatRole,
				Content: content,
			})
			continue
		}

		if itemType == "function_call" {
			callID, _ := item["call_id"].(string)
			if callID == "" {
				if id, _ := item["id"].(string); id != "" {
					callID = id
				}
			}
			name, _ := item["name"].(string)
			if name == "" {
				name = "unknown_tool"
			}
			args := extractArguments(item["arguments"])
			if args == "" {
				args = "{}"
			}

			pendingToolCalls = append(pendingToolCalls, dto.ToolCallRequest{
				ID:   callID,
				Type: "function",
				Function: dto.FunctionRequest{
					Name:      name,
					Arguments: args,
				},
			})
			continue
		}

		if itemType == "function_call_output" {
			flushToolCalls()

			callID, _ := item["call_id"].(string)
			if callID == "" {
				if id, _ := item["id"].(string); id != "" {
					callID = id
				}
			}
			output := extractContentText(item["output"])
			messages = append(messages, dto.Message{
				Role:       "tool",
				ToolCallId: callID,
				Content:    output,
			})
			continue
		}

		flushToolCalls()
		text := extractContentText(item["content"])
		if text == "" {
			if raw, err := common.Marshal(item); err == nil {
				text = string(raw)
			}
		}
		if text != "" {
			messages = append(messages, dto.Message{
				Role:    "user",
				Content: text,
			})
		}
	}

	flushToolCalls()

	return messages, nil
}

func extractReasoningFromItem(item map[string]any) string {
	// Primary: "content" field — va-ai-api-bridge format:
	//   {"type":"reasoning","content":[{"type":"reasoning_text","text":"..."}]}
	if content, ok := item["content"].([]any); ok {
		for _, c := range content {
			if m, ok := c.(map[string]any); ok {
				if m["type"] == "reasoning_text" || m["type"] == "summary_text" {
					if text, ok := m["text"].(string); ok && text != "" {
						return text
					}
				}
			}
		}
	}
	// Fallback: legacy "summary" field
	if summary, ok := item["summary"].([]any); ok {
		for _, s := range summary {
			if m, ok := s.(map[string]any); ok {
				if m["type"] == "summary_text" {
					if text, ok := m["text"].(string); ok && text != "" {
						return text
					}
				}
			}
		}
	}
	// Extension: reasoning_content from Chat API message.extra
	if rc, ok := item["reasoning_content"].(string); ok && rc != "" {
		return rc
	}
	return ""
}

func extractArguments(arg any) string {
	if arg == nil {
		return ""
	}
	switch v := arg.(type) {
	case string:
		return v
	default:
		// marshal to JSON string
		if b, err := common.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", v)
	}
}

func extractContentText(content any) string {
	if content == nil {
		return ""
	}
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, part := range c {
			if s, ok := part.(string); ok {
				parts = append(parts, s)
			} else if m, ok := part.(map[string]any); ok {
				partType, _ := m["type"].(string)
				if partType == "input_text" || partType == "output_text" || partType == "text" {
					if text, ok := m["text"].(string); ok {
						parts = append(parts, text)
					}
				} else if text, ok := m["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return fmt.Sprintf("%v", c)
	}
}

func responsesToolsToChatTools(tools []map[string]any) []dto.ToolCallRequest {
	var out []dto.ToolCallRequest
	for _, tool := range tools {
		toolType, _ := tool["type"].(string)
		if toolType == "" {
			continue
		}
		if toolType != "function" {
			continue
		}
		// Responses format: {type, name, description, parameters}
		// Chat format: {type, function: {name, description, parameters}}
		name, _ := tool["name"].(string)
		if name == "" {
			if fn, ok := tool["function"].(map[string]any); ok {
				name, _ = fn["name"].(string)
			}
		}
		if name == "" {
			continue
		}
		description, _ := tool["description"].(string)
		params := tool["parameters"]
		if params == nil {
			if fn, ok := tool["function"].(map[string]any); ok {
				params = fn["parameters"]
			}
		}

		out = append(out, dto.ToolCallRequest{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:        name,
				Description: description,
				Parameters:  params,
			},
		})
	}
	return out
}

func responsesToolChoiceToChatToolChoice(toolChoiceRaw json.RawMessage) any {
	var toolChoiceStr string
	if err := common.Unmarshal(toolChoiceRaw, &toolChoiceStr); err == nil {
		switch strings.TrimSpace(toolChoiceStr) {
		case "auto", "none", "required":
			return toolChoiceStr
		}
	}

	var toolChoiceMap map[string]any
	if err := common.Unmarshal(toolChoiceRaw, &toolChoiceMap); err != nil {
		return nil
	}

	tcType, _ := toolChoiceMap["type"].(string)
	if tcType == "function" {
		name, _ := toolChoiceMap["name"].(string)
		if name != "" {
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": name,
				},
			}
		}
	}
	return nil
}
