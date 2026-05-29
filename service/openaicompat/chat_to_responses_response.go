package openaicompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

func newResponsesID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano()%1000000000)
}

func ChatCompletionsResponseToResponsesResponse(chatResp *dto.OpenAITextResponse, model string) (*dto.OpenAIResponsesResponse, *dto.Usage, error) {
	if chatResp == nil {
		return nil, nil, errors.New("response is nil")
	}
	if len(chatResp.Choices) == 0 {
		return nil, nil, errors.New("no choices in response")
	}

	createdAt := 0
	switch v := chatResp.Created.(type) {
	case float64:
		createdAt = int(v)
	case int64:
		createdAt = int(v)
	case int:
		createdAt = v
	default:
		createdAt = int(time.Now().Unix())
	}

	choice := chatResp.Choices[0]
	message := choice.Message

	var output []dto.ResponsesOutput

	// DeepSeek thinking mode requires reasoning_content passthrough in multi-turn conversations.
	reasoning := message.GetReasoningContent()
	if reasoning != "" {
		output = append(output, dto.ResponsesOutput{
			ID:      newResponsesID("rs"),
			Type:    "reasoning",
			Status:  "completed",
			Content: []dto.ResponsesOutputContent{{Type: "reasoning_text", Text: reasoning}},
			Summary: json.RawMessage("[]"),
		})
	}

	text := ""
	if message.IsStringContent() {
		text = message.StringContent()
	} else if message.Content != nil {
		text = fmt.Sprintf("%v", message.Content)
	}

	hasToolCalls := len(message.ParseToolCalls()) > 0
	if text != "" || !hasToolCalls {
		output = append(output, dto.ResponsesOutput{
			ID:     newResponsesID("msg"),
			Type:   "message",
			Status: "completed",
			Role:   "assistant",
			Content: []dto.ResponsesOutputContent{
				{
					Type:        "output_text",
					Text:        text,
					Annotations: []interface{}{},
				},
			},
		})
	}
	for _, tc := range message.ParseToolCalls() {
		name := tc.Function.Name
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		// arguments must be a JSON-encoded STRING, not a JSON object
		argumentsJSON, _ := common.Marshal(args)
		output = append(output, dto.ResponsesOutput{
			ID:        newResponsesID("fc"),
			Type:      "function_call",
			Status:    "completed",
			CallId:    tc.ID,
			Name:      name,
			Arguments: json.RawMessage(argumentsJSON),
		})
	}

	usage := &dto.Usage{
		InputTokens:  chatResp.Usage.PromptTokens,
		OutputTokens: chatResp.Usage.CompletionTokens,
		TotalTokens:  chatResp.Usage.TotalTokens,
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	statusRaw, _ := common.Marshal("completed")

	out := &dto.OpenAIResponsesResponse{
		ID:        chatResp.Id,
		Object:    "response",
		CreatedAt: createdAt,
		Status:    statusRaw,
		Model:     chatResp.Model,
		Output:    output,
		Usage:     usage,
	}

	if model != "" {
		out.Model = model
	}

	return out, usage, nil
}
