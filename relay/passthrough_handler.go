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
