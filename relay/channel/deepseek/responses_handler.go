package deepseek

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	openaicompat "github.com/QuantumNous/new-api/service/openaicompat"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const maxToolCallsPerResponse = 128

func DeepSeekOaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	var chatResp dto.OpenAITextResponse
	if err := common.Unmarshal(body, &chatResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	body = nil // allow GC during conversion

	if oaiError := chatResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	if chatResp.Id == "" {
		chatResp.Id = helper.GetResponseID(c)
	}

	responsesResp, usage, err := openaicompat.ChatCompletionsResponseToResponsesResponse(&chatResp, info.UpstreamModelName)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if usage == nil || usage.TotalTokens == 0 {
		text := openaicompat.ExtractOutputTextFromResponses(responsesResp)
		usage = service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
	}

	responseBody, err := common.Marshal(responsesResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

type streamItemState struct {
	idx     int
	builder strings.Builder
	callID  string
	name    string
	itemID  string
}

func DeepSeekOaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	responseId := helper.GetResponseID(c)
	createdAt := time.Now().Unix()
	model := info.UpstreamModelName

	var (
		usage          = &dto.Usage{}
		output         []dto.ResponsesOutput
		msgIdx         = -1
		textState      *streamItemState
		reasoningState *streamItemState
		toolStates     []*streamItemState
		accumulatedText string

	)

	sendSSE := func(event string, data map[string]any) bool {
		data["type"] = event
		jsonData, err := common.Marshal(data)
		if err != nil {
			return false
		}
		streamResp := dto.ResponsesStreamResponse{Type: event}
		helper.ResponseChunkData(c, streamResp, string(jsonData))
		return true
	}

	helper.SetEventStreamHeaders(c)
	_ = helper.FlushWriter(c)

	statusInProgress, _ := common.Marshal("in_progress")
	toolChoiceAuto, _ := common.Marshal("auto")
	shellResponse := &dto.OpenAIResponsesResponse{
		ID:                responseId,
		Object:            "response",
		CreatedAt:         int(createdAt),
		Status:            statusInProgress,
		Model:             model,
		Output:            []dto.ResponsesOutput{},
		ParallelToolCalls: true,
		ToolChoice:        toolChoiceAuto,
		Tools:             []map[string]any{},
	}
	sendSSE("response.created", map[string]any{"response": shellResponse})
	sendSSE("response.in_progress", map[string]any{"response": shellResponse})

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			return
		}

		if len(chunk.Choices) == 0 {
			if chunk.Usage != nil && chunk.Usage.TotalTokens > 0 {
				usage.PromptTokens = chunk.Usage.PromptTokens
				usage.CompletionTokens = chunk.Usage.CompletionTokens
				usage.TotalTokens = chunk.Usage.TotalTokens
			}
			return
		}

		choice := chunk.Choices[0]
		delta := choice.Delta

		if r := delta.GetReasoningContent(); r != "" {
			if reasoningState == nil {
				reasoningState = &streamItemState{idx: len(output)}
				reasoningItem := dto.ResponsesOutput{
					ID:     fmt.Sprintf("rs_%s_%d", responseId, reasoningState.idx),
					Type:   "reasoning",
					Status: "in_progress",
				}
				output = append(output, reasoningItem)
				sendSSE("response.output_item.added", map[string]any{
					"output_index": reasoningState.idx,
					"item":         reasoningItem,
				})
			}
			reasoningState.builder.WriteString(r)
			sendSSE("response.reasoning_text.delta", map[string]any{
				"item_id":      output[reasoningState.idx].ID,
				"output_index": reasoningState.idx,
				"delta":        r,
			})
			return
		}

		if content := delta.GetContentString(); content != "" {
			if textState == nil {
				msgIdx = len(output)
				textState = &streamItemState{idx: msgIdx}
				textItem := dto.ResponsesOutput{
					ID:      fmt.Sprintf("msg_%s_%d", responseId, msgIdx),
					Type:    "message",
					Status:  "in_progress",
					Role:    "assistant",
					Content: []dto.ResponsesOutputContent{},
				}
				output = append(output, textItem)
				sendSSE("response.output_item.added", map[string]any{
					"output_index": msgIdx,
					"item":         textItem,
				})
				sendSSE("response.content_part.added", map[string]any{
					"item_id":       textItem.ID,
					"output_index":  msgIdx,
					"content_index": 0,
					"part": map[string]any{
						"type": "output_text",
						"text": "",
					},
				})
			}
			textState.builder.WriteString(content)
			sendSSE("response.output_text.delta", map[string]any{
				"item_id":       output[textState.idx].ID,
				"output_index":  msgIdx,
				"content_index": 0,
				"delta":         content,
			})
		}

		for _, tc := range delta.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			if idx > maxToolCallsPerResponse {
				continue
			}
			for len(toolStates) <= idx {
				toolStates = append(toolStates, nil)
			}
			if toolStates[idx] == nil {
				toolOutputIdx := len(output)
				name := tc.Function.Name
				callID := tc.ID
				itemID := fmt.Sprintf("fc_%s_%d", responseId, idx)
				toolItem := dto.ResponsesOutput{
					ID:     itemID,
					Type:   "function_call",
					Status: "in_progress",
					CallId: callID,
					Name:   name,
				}
				output = append(output, toolItem)
				toolStates[idx] = &streamItemState{
					idx:    toolOutputIdx,
					callID: callID,
					name:   name,
					itemID: itemID,
				}
				sendSSE("response.output_item.added", map[string]any{
					"output_index": toolOutputIdx,
					"item": map[string]any{
						"id":      itemID,
						"type":    "function_call",
						"status":  "in_progress",
						"call_id": callID,
						"name":    name,
					},
				})
			}
			if tc.Function.Name != "" {
				toolStates[idx].name = tc.Function.Name
				output[toolStates[idx].idx].Name = tc.Function.Name
			}
			if tc.ID != "" && toolStates[idx].callID == "" {
				toolStates[idx].callID = tc.ID
				output[toolStates[idx].idx].CallId = tc.ID
			}
			if tc.Function.Arguments != "" {
				toolStates[idx].builder.WriteString(tc.Function.Arguments)
				sendSSE("response.function_call_arguments.delta", map[string]any{
					"item_id":      toolStates[idx].itemID,
					"output_index": toolStates[idx].idx,
					"delta":        tc.Function.Arguments,
				})
			}
		}

		if choice.FinishReason != nil && *choice.FinishReason != "" {
			if chunk.Usage != nil && chunk.Usage.TotalTokens > 0 {
				usage.PromptTokens = chunk.Usage.PromptTokens
				usage.CompletionTokens = chunk.Usage.CompletionTokens
				usage.TotalTokens = chunk.Usage.TotalTokens
			}
		}
	})

	// Finalize reasoning
	if reasoningState != nil {
		ri := reasoningState.idx
		fullReasoning := reasoningState.builder.String()
		output[ri].Content = []dto.ResponsesOutputContent{
			{Type: "reasoning_text", Text: fullReasoning},
		}
		output[ri].Summary = json.RawMessage("[]")
		output[ri].Status = "completed"
		sendSSE("response.reasoning_text.done", map[string]any{
			"text":         fullReasoning,
			"output_index": ri,
		})
		sendSSE("response.output_item.done", map[string]any{
			"output_index": ri,
			"item":         output[ri],
		})
	}

	// Finalize text
	if textState != nil {
		ti := textState.idx
		accumulatedText = textState.builder.String()
		output[ti].Content = []dto.ResponsesOutputContent{
			{Type: "output_text", Text: accumulatedText, Annotations: []interface{}{}},
		}
		output[ti].Status = "completed"

		sendSSE("response.output_text.done", map[string]any{
			"item_id":       output[ti].ID,
			"output_index":  msgIdx,
			"content_index": 0,
			"text":          accumulatedText,
		})
		sendSSE("response.content_part.done", map[string]any{
			"item_id":       output[ti].ID,
			"output_index":  msgIdx,
			"content_index": 0,
			"part": map[string]any{
				"type": "output_text",
				"text": accumulatedText,
			},
		})
		sendSSE("response.output_item.done", map[string]any{
			"output_index": msgIdx,
			"item":         output[ti],
		})
	}

	// Finalize tool calls
	for _, ts := range toolStates {
		if ts == nil {
			continue
		}
		ti := ts.idx
		args := ts.builder.String()
		if args == "" {
			args = "{}"
		}
		if argsJSON, err := common.Marshal(args); err == nil {
			output[ti].Arguments = argsJSON
		}
		output[ti].Status = "completed"
		output[ti].Name = ts.name
		output[ti].CallId = ts.callID

		sendSSE("response.function_call_arguments.done", map[string]any{
			"item_id":      ts.itemID,
			"output_index": ti,
			"arguments":    args,
		})
		sendSSE("response.output_item.done", map[string]any{
			"output_index": ti,
			"item":         output[ti],
		})
	}

	// Fallback: empty message when there's no output
	if textState == nil && len(toolStates) == 0 && reasoningState == nil {
		emptyIdx := len(output)
		emptyItem := dto.ResponsesOutput{
			ID:      fmt.Sprintf("msg_%s_empty", responseId),
			Type:    "message",
			Status:  "completed",
			Role:    "assistant",
			Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "", Annotations: []interface{}{}}},
		}
		output = append(output, emptyItem)
		sendSSE("response.output_item.added", map[string]any{"output_index": emptyIdx, "item": emptyItem})
		sendSSE("response.output_item.done", map[string]any{"output_index": emptyIdx, "item": emptyItem})
	}

	if usage.TotalTokens == 0 {
		if len(accumulatedText) > 0 {
			usage.CompletionTokens = service.CountTextToken(accumulatedText, info.UpstreamModelName)
		}
		if usage.PromptTokens == 0 {
			usage.PromptTokens = info.GetEstimatePromptTokens()
		}
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	shellResponse.Status, _ = common.Marshal("completed")
	shellResponse.Output = output
	shellResponse.Usage = &dto.Usage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
	}
	sendSSE("response.completed", map[string]any{"response": shellResponse})

	return usage, nil
}
