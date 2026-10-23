package service

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// GetPollingClientKey 返回渠道轮询的客户端 key;"user" 维度用 user id,"token" 维度用 token id。
// c 为 nil 时返回空串(调用方不启用轮询)。
func GetPollingClientKey(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if setting.ChannelPollingScope == setting.ChannelPollingScopeToken {
		return "t:" + strconv.Itoa(common.GetContextKeyInt(c, constant.ContextKeyTokenId))
	}
	return "u:" + strconv.Itoa(c.GetInt("id"))
}
