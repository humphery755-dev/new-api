package model

import (
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/setting"
)

// swrrState 保存单个 (clientKey|model) 的平滑加权轮询游标。
type swrrState struct {
	signature string
	weights   map[int]int // channelId -> currentWeight
}

var (
	pollingStates   = map[string]*swrrState{}
	pollingStatesMu sync.Mutex
)

// maxPollingStates 是轮询状态的容量上限;超限时整体清零。
// 轮询游标只影响分发顺序,不影响正确性,清零可接受。
const maxPollingStates = 10000

// pollingCandidateSignature 是候选集签名;渠道集合或权重变化后签名失配即重建 state。
func pollingCandidateSignature(channels []*Channel) string {
	var b strings.Builder
	for _, ch := range channels {
		b.WriteString(strconv.Itoa(ch.Id))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(ch.GetWeight()))
		b.WriteByte(',')
	}
	return b.String()
}

// selectChannelByPolling 在同优先级候选渠道内做平滑加权轮询(SWRR)。
// clientKey 为空或开关关闭时返回 nil,调用方回落加权随机路径。
// 零/负权重渠道以权重 1 参与均等轮换(与随机路径的全零平滑语义一致)。
// 轮询状态为 per-instance(进程内存量),单实例部署精确生效。
func selectChannelByPolling(clientKey, modelName string, channels []*Channel) *Channel {
	if len(channels) == 0 || clientKey == "" || !setting.ChannelPollingEnabled {
		return nil
	}

	signature := pollingCandidateSignature(channels)
	pollingStatesMu.Lock()
	defer pollingStatesMu.Unlock()

	key := clientKey + "|" + modelName
	st, ok := pollingStates[key]
	if !ok || st.signature != signature {
		st = &swrrState{signature: signature, weights: make(map[int]int, len(channels))}
		if len(pollingStates) >= maxPollingStates {
			pollingStates = map[string]*swrrState{}
		}
		pollingStates[key] = st
	}

	total := 0
	for _, ch := range channels {
		w := ch.GetWeight()
		if w <= 0 {
			w = 1
		}
		total += w
		st.weights[ch.Id] += w
	}

	var selected *Channel
	for _, ch := range channels {
		if selected == nil || st.weights[ch.Id] > st.weights[selected.Id] {
			selected = ch
		}
	}
	if selected == nil {
		return nil
	}
	st.weights[selected.Id] -= total
	return selected
}
