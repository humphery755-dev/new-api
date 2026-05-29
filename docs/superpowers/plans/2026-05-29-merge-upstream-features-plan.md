# Merge Upstream Features Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge 3 features (Passthrough Handler, Coding Plan VLM, DeepSeek Responses API) from upstream `/home/humphery/workspace/new-api` into current fork.

**Architecture:** File-by-file diff merge in 3 dependency-ordered batches. Each batch copies or patches files from the upstream source, then verifies compilation. No new tests are written since upstream doesn't have tests for these features either.

**Tech Stack:** Go 1.22+, Git

---

### Task 1: Batch A1 — Add DeleteJSONField to common/json.go

**Files:**
- Modify: `common/json.go` (append function at end of file)

- [ ] **Step 1: Append DeleteJSONField function**

Read the current file to confirm the end location, then append:

```go
// DeleteJSONField removes a key from a JSON object. Returns the unchanged data if
// the input is not a JSON object or the key is not present.
func DeleteJSONField(data []byte, key string) []byte {
	var m map[string]json.RawMessage
	if err := Unmarshal(data, &m); err != nil {
		return data
	}
	if _, ok := m[key]; !ok {
		return data
	}
	delete(m, key)
	result, err := Marshal(m)
	if err != nil {
		return data
	}
	return result
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./common/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add common/json.go
git commit -m "feat: add DeleteJSONField utility function

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 2: Batch A2 — Add passthrough_handler.go

**Files:**
- Create: `relay/passthrough_handler.go`

- [ ] **Step 1: Create the file**

Copy verbatim from `/home/humphery/workspace/new-api/relay/passthrough_handler.go`:

```go
package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func PassthroughHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	return passthroughHelper(c, info, false)
}

func VlmPassthroughHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	return passthroughHelper(c, info, true)
}

func passthroughHelper(c *gin.Context, info *relaycommon.RelayInfo, stripModel bool) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(
			fmt.Errorf("invalid api type: %d", info.ApiType),
			types.ErrorCodeInvalidApiType,
			types.ErrOptionWithSkipRetry(),
		)
	}
	adaptor.Init(info)

	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
	}

	var requestBody io.Reader
	if stripModel {
		data, readErr := io.ReadAll(common.ReaderOnly(storage))
		if readErr != nil {
			return types.NewError(readErr, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		if bytes.Contains(data, []byte(`"model"`)) {
			data = common.DeleteJSONField(data, "model")
		}
		requestBody = bytes.NewReader(data)
	} else {
		requestBody = common.ReaderOnly(storage)
	}

	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		if httpResp.StatusCode != http.StatusOK {
			newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
			service.ResetStatusCode(newAPIError, statusCodeMappingStr)
			return newAPIError
		}
	}

	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	if usage != nil {
		if u, ok := usage.(*dto.Usage); ok {
			service.PostTextConsumeQuota(c, info, u, nil)
		}
	}
	return nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./relay/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add relay/passthrough_handler.go
git commit -m "feat: add passthrough handler for generic upstream relay

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 3: Batch A3 — Add Summary field to dto/openai_response.go

**Files:**
- Modify: `dto/openai_response.go` (add field to `ResponsesOutputContent` struct)

- [ ] **Step 1: Find and modify the ResponsesOutputContent struct**

Find this struct (around line 346):

```go
type ResponsesOutputContent struct {
	Type        string                   `json:"type"`
	Text        string                   `json:"text,omitempty"`
	Annotations []interface{}            `json:"annotations,omitempty"`
}
```

Add the `Summary` field before the closing brace:

```go
type ResponsesOutputContent struct {
	Type        string                   `json:"type"`
	Text        string                   `json:"text,omitempty"`
	Annotations []interface{}            `json:"annotations,omitempty"`
	Summary     json.RawMessage          `json:"summary,omitempty"`
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./dto/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add dto/openai_response.go
git commit -m "feat: add Summary field to ResponsesOutputContent

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 4: Batch B1 — Add relay_mode constants and path parsing

**Files:**
- Modify: `relay/constant/relay_mode.go`

- [ ] **Step 1: Add new relay mode constants**

After `RelayModeParsePdf` (around line 57), add:

```go

	RelayModeCodingPlanVLM
	RelayModeCodingPlanSearch
```

- [ ] **Step 2: Add DefaultCodingPlanVLMModel constant**

After the new relay modes:

```go
const DefaultCodingPlanVLMModel = "MiniMax-M2.7"
```

- [ ] **Step 3: Add path parsing in GetRelayMode**

Find the `if strings.HasPrefix(path, "...")` chain (around line 84). After the last existing `} else if` block and before the final `return`, add:

```go
	} else if strings.HasPrefix(path, "/v1/coding_plan/search") {
		relayMode = RelayModeCodingPlanSearch
	} else if strings.HasPrefix(path, "/v1/coding_plan/vlm") {
		relayMode = RelayModeCodingPlanVLM
```

- [ ] **Step 4: Verify compilation**

Run: `go build ./relay/constant/...`
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add relay/constant/relay_mode.go
git commit -m "feat: add CodingPlan VLM relay mode constants and routing

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 5: Batch B2 — Create MiniMax coding_plan_vlm handler

**Files:**
- Create: `relay/channel/minimax/coding_plan_vlm.go`

- [ ] **Step 1: Create the file**

```go
package minimax

import (
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func codingPlanVLMHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	for k, v := range resp.Header {
		for _, vv := range v {
			c.Writer.Header().Add(k, vv)
		}
	}
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := c.Writer.Write(body); err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	return &dto.Usage{}, nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./relay/channel/minimax/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add relay/channel/minimax/coding_plan_vlm.go
git commit -m "feat: add MiniMax coding_plan VLM passthrough handler

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 6: Batch B3 — Modify MiniMax adaptor and relay for Coding Plan VLM

**Files:**
- Modify: `relay/channel/minimax/adaptor.go`
- Modify: `relay/channel/minimax/relay-minimax.go`

- [ ] **Step 1: Add DoResponse dispatch in adaptor.go**

In `relay/channel/minimax/adaptor.go`, find the `DoResponse` function. Before the final return or default branch, add:

```go
	if info.RelayMode == constant.RelayModeCodingPlanVLM ||
		info.RelayMode == constant.RelayModeCodingPlanSearch {
		return codingPlanVLMHandler(c, resp, info)
	}
```

- [ ] **Step 2: Add URL routing in relay-minimax.go**

In `relay/channel/minimax/relay-minimax.go`, find the `GetRequestURL` function's switch statement. Add cases for the coding plan modes:

```go
		case constant.RelayModeCodingPlanVLM:
			return fmt.Sprintf("%s/v1/coding_plan/vlm", baseUrl), nil
		case constant.RelayModeCodingPlanSearch:
			return fmt.Sprintf("%s/v1/coding_plan/search", baseUrl), nil
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./relay/channel/minimax/...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add relay/channel/minimax/adaptor.go relay/channel/minimax/relay-minimax.go
git commit -m "feat: wire MiniMax coding_plan VLM into adaptor and URL routing

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 7: Batch B4 — Add default model routing and valid request for Coding Plan VLM

**Files:**
- Modify: `middleware/distributor.go`
- Modify: `relay/helper/valid_request.go`
- Modify: `controller/relay.go`
- Modify: `router/relay-router.go`

- [ ] **Step 1: Add default model in distributor.go**

In `middleware/distributor.go`, in the `Distribute` function, find the model routing section (around line 343). Add after the last `if strings.HasPrefix` block:

```go
	if strings.HasPrefix(c.Request.URL.Path, "/v1/coding_plan/vlm") ||
		strings.HasPrefix(c.Request.URL.Path, "/v1/coding_plan/search") {
		modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, relayconstant.DefaultCodingPlanVLMModel)
	}
```

- [ ] **Step 2: Add valid request in valid_request.go**

In `relay/helper/valid_request.go`, find the `GetAndValidRequest` function. After the `return nil, nil` paths and before the main logic, add:

```go
	if relayMode == relayconstant.RelayModeCodingPlanVLM ||
		relayMode == relayconstant.RelayModeCodingPlanSearch {
		return &dto.GeneralOpenAIRequest{
			Model: relayconstant.DefaultCodingPlanVLMModel,
		}, nil
	}
```

- [ ] **Step 3: Add controller relay dispatch**

In `controller/relay.go`, in the `Relay` function's switch/case for relay mode (around line 52-53), add after the existing cases:

```go
	case relayconstant.RelayModeCodingPlanVLM, relayconstant.RelayModeCodingPlanSearch:
		err = relay.VlmPassthroughHelper(c, info)
```

- [ ] **Step 4: Add routes in relay-router.go**

In `router/relay-router.go`, after the existing POST route registrations (around line 135), add:

```go
		// coding_plan_vlm route (MiniMax)
		httpRouter.POST("/coding_plan/vlm", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})

		// coding_plan_search route (MiniMax)
		httpRouter.POST("/coding_plan/search", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})
```

- [ ] **Step 5: Verify compilation**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add middleware/distributor.go relay/helper/valid_request.go controller/relay.go router/relay-router.go
git commit -m "feat: add Coding Plan VLM routes, model routing, and relay dispatch

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 8: Batch C1 — Create openaicompat package

**Files:**
- Create: `service/openaicompat/chat_to_responses_response.go`
- Create: `service/openaicompat/responses_to_chat_request.go`

- [ ] **Step 1: Create chat_to_responses_response.go**

Run: `cp /home/humphery/workspace/new-api/service/openaicompat/chat_to_responses_response.go service/openaicompat/chat_to_responses_response.go`

- [ ] **Step 2: Create responses_to_chat_request.go**

Run: `cp /home/humphery/workspace/new-api/service/openaicompat/responses_to_chat_request.go service/openaicompat/responses_to_chat_request.go`

- [ ] **Step 3: Ensure samber/lo dependency**

Run: `go get github.com/samber/lo 2>&1 || true`
Expected: dependency added or already present

- [ ] **Step 4: Verify compilation**

Run: `go build ./service/openaicompat/...`
Expected: no errors. If `samber/lo` is not in go.mod, run `go get github.com/samber/lo`

- [ ] **Step 5: Commit**

```bash
git add service/openaicompat/
git commit -m "feat: add OpenAI compat layer for Chat↔Responses conversion

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 9: Batch C2 — Create DeepSeek responses handler

**Files:**
- Create: `relay/channel/deepseek/responses_handler.go`

- [ ] **Step 1: Create the file**

Run: `cp /home/humphery/workspace/new-api/relay/channel/deepseek/responses_handler.go relay/channel/deepseek/responses_handler.go`

This copies the complete 381-line DeepSeek Responses API stream/non-stream handler.

- [ ] **Step 2: Verify compilation**

Run: `go build ./relay/channel/deepseek/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add relay/channel/deepseek/responses_handler.go
git commit -m "feat: add DeepSeek Responses API stream and non-stream handlers

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 10: Batch C3 — Wire DeepSeek adaptor and responses_handler

**Files:**
- Modify: `relay/channel/deepseek/adaptor.go`
- Modify: `relay/responses_handler.go`

- [ ] **Step 1: Modify deepseek adaptor ConvertRequest**

In `relay/channel/deepseek/adaptor.go`:

Replace the existing `ConvertRequest` implementation (which returns `errors.New("not implemented")`) with:

```go
	converted, err := openaicompat.ResponsesRequestToChatCompletionsRequest(&request)
	if err != nil {
		return nil, err
	}
	if request.Stream != nil && *request.Stream {
		info.IsStream = true
	}
	return converted, nil
```

Also add the import for `openaicompat`:
```go
	openaicompat "github.com/QuantumNous/new-api/service/openaicompat"
```

- [ ] **Step 2: Modify deepseek adaptor DoResponse**

In `DoResponse`, before the default return, add:

```go
		if info.RelayMode == constant.RelayModeResponses || info.RelayMode == constant.RelayModeResponsesCompact {
			if info.IsStream {
				return DeepSeekOaiResponsesStreamHandler(c, info, resp)
			}
			return DeepSeekOaiResponsesHandler(c, info, resp)
		}
```

- [ ] **Step 3: Add DeepSeek type to responses_handler.go**

In `relay/responses_handler.go`, add `appconstant.APITypeDeepSeek` to the switch case:

Find: `case appconstant.APITypeOpenAI, appconstant.APITypeCodex:`
Change to: `case appconstant.APITypeOpenAI, appconstant.APITypeCodex, appconstant.APITypeDeepSeek:`

- [ ] **Step 4: Verify compilation**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 5: Run go vet**

Run: `go vet ./...`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add relay/channel/deepseek/adaptor.go relay/responses_handler.go
git commit -m "feat: wire DeepSeek Responses API into adaptor and relay handler

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 11: Final verification

- [ ] **Step 1: Full build**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 2: Full vet**

Run: `go vet ./...`
Expected: no errors
