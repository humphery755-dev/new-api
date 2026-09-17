package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pollingTestChannel(id int, weight uint) *Channel {
	return &Channel{Id: id, Weight: &weight}
}

func setupPollingFixture(t *testing.T, enabled bool, scope string) {
	t.Helper()
	origEnabled, origScope := setting.ChannelPollingEnabled, setting.ChannelPollingScope
	setting.ChannelPollingEnabled = enabled
	setting.ChannelPollingScope = scope
	t.Cleanup(func() {
		setting.ChannelPollingEnabled = origEnabled
		setting.ChannelPollingScope = origScope
		pollingStatesMu.Lock()
		pollingStates = map[string]*swrrState{}
		pollingStatesMu.Unlock()
	})
}

func TestSelectChannelByPollingWeightTwoToOne(t *testing.T) {
	setupPollingFixture(t, true, "user")
	channels := []*Channel{pollingTestChannel(11, 2), pollingTestChannel(22, 1)}

	var got []int
	for range 6 {
		ch := selectChannelByPolling("u:1", "L2-ALL", channels)
		require.NotNil(t, ch)
		got = append(got, ch.Id)
	}
	assert.Equal(t, []int{11, 22, 11, 11, 22, 11}, got, "SWRR 2:1 deterministic sequence")
}

func TestSelectChannelByPollingPerClientIndependence(t *testing.T) {
	setupPollingFixture(t, true, "user")
	channels := []*Channel{pollingTestChannel(11, 2), pollingTestChannel(22, 1)}

	// 客户端 A 消费两轮后,客户端 B 的序列仍从头开始
	for range 2 {
		selectChannelByPolling("u:1", "L2-ALL", channels)
	}
	first := selectChannelByPolling("u:2", "L2-ALL", channels)
	require.NotNil(t, first)
	assert.Equal(t, 11, first.Id)
}

func TestSelectChannelByPollingRebuildsOnCandidateChange(t *testing.T) {
	setupPollingFixture(t, true, "user")
	channels := []*Channel{pollingTestChannel(11, 2), pollingTestChannel(22, 1)}
	selectChannelByPolling("u:1", "L2-ALL", channels)

	// 渠道 22 禁用移除后,候选集变化 → state 重建,11 独占
	newChannels := []*Channel{pollingTestChannel(11, 2)}
	got := selectChannelByPolling("u:1", "L2-ALL", newChannels)
	require.NotNil(t, got)
	assert.Equal(t, 11, got.Id)

	pollingStatesMu.Lock()
	defer pollingStatesMu.Unlock()
	st, ok := pollingStates["u:1|L2-ALL"]
	require.True(t, ok)
	assert.Equal(t, "11:2,", st.signature)
}

func TestSelectChannelByPollingZeroWeightRotatesEvenly(t *testing.T) {
	setupPollingFixture(t, true, "user")
	channels := []*Channel{pollingTestChannel(11, 0), pollingTestChannel(22, 0)}

	var got []int
	for range 4 {
		ch := selectChannelByPolling("u:1", "L2-ALL", channels)
		require.NotNil(t, ch)
		got = append(got, ch.Id)
	}
	assert.Equal(t, []int{11, 22, 11, 22}, got)
}

func TestSelectChannelByPollingDisabledReturnsNil(t *testing.T) {
	setupPollingFixture(t, false, "user")
	channels := []*Channel{pollingTestChannel(11, 2)}
	assert.Nil(t, selectChannelByPolling("u:1", "L2-ALL", channels))
	assert.Nil(t, selectChannelByPolling("", "L2-ALL", channels), "empty clientKey never polls")
}
