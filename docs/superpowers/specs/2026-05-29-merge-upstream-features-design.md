# Merge Features from new-api to new-api-dev

Date: 2026-05-29
Source: `/home/humphery/workspace/new-api` (upstream)
Target: `/home/humphery/workspace/new-api-dev` (fork, branch `v1alpha0.1`)

## Overview

Merge 3 features from upstream into the fork: Passthrough Handler, Coding Plan VLM, and DeepSeek Responses API.

## Merge Strategy

Manual file-by-file merge in 3 batches ordered by dependency.

## Batch A: Infrastructure (2 new, 2 modified)

1. **`common/json.go`** — Append `DeleteJSONField` function (remove a key from JSON object)
2. **`dto/openai_response.go`** — Add `Summary` field to `ResponsesOutputContent` struct
3. **`relay/passthrough_handler.go`** — NEW: `PassthroughHelper` + `VlmPassthroughHelper` (generic upstream relay with optional model stripping)

## Batch B: Coding Plan VLM (1 new, 7 modified)

1. **`relay/channel/minimax/coding_plan_vlm.go`** — NEW: `codingPlanVLMHandler` (passthrough body+headers to upstream)
2. **`relay/constant/relay_mode.go`** — Add `RelayModeCodingPlanVLM`, `RelayModeCodingPlanSearch` and `DefaultCodingPlanVLMModel = "MiniMax-M2.7"`; add path parsing for `/v1/coding_plan/*`
3. **`relay/channel/minimax/adaptor.go`** — `DoResponse`: dispatch to `codingPlanVLMHandler` for coding plan modes
4. **`relay/channel/minimax/relay-minimax.go`** — `GetRequestURL`: route coding plan modes to `/v1/coding_plan/vlm` or `/coding_plan/search`
5. **`relay/helper/valid_request.go`** — `GetAndValidRequest`: return empty request with default model for coding plan modes
6. **`controller/relay.go`** — `Relay`: call `VlmPassthroughHelper` for coding plan modes
7. **`middleware/distributor.go`** — `Distribute`: set default model for coding_plan paths
8. **`router/relay-router.go`** — Register POST `/coding_plan/vlm` and `/coding_plan/search` routes

## Batch C: DeepSeek Responses API (3 new, 2 modified)

1. **`service/openaicompat/responses_to_chat_request.go`** — NEW: `ResponsesRequestToChatCompletionsRequest` converter
2. **`service/openaicompat/chat_to_responses_response.go`** — NEW: `ChatCompletionsResponseToResponsesResponse` converter
3. **`relay/channel/deepseek/responses_handler.go`** — NEW: `DeepSeekOaiResponsesHandler` + `DeepSeekOaiResponsesStreamHandler` (both stream and non-stream)
4. **`relay/channel/deepseek/adaptor.go`** — `ConvertRequest`: use `ResponsesRequestToChatCompletionsRequest`; `DoResponse`: dispatch responses mode to DeepSeek handlers
5. **`relay/responses_handler.go`** — Add `APITypeDeepSeek` to responses handler switch

## Verification

- `go build ./...` must succeed after each batch
- `go vet ./...` must pass after merge complete
