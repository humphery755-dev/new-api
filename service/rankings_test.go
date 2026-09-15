package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/model"
)

func TestBuildRankedUsers(t *testing.T) {
	tests := []struct {
		name        string
		totals      []model.RankingUserTotal
		totalTokens int64
		expected    []RankedUser
	}{
		{
			name: "assigns sequential ranks and share of total",
			totals: []model.RankingUserTotal{
				{UserID: 1, Username: "alice", TotalTokens: 600},
				{UserID: 2, Username: "bob", TotalTokens: 400},
			},
			totalTokens: 1000,
			expected: []RankedUser{
				{Rank: 1, UserID: 1, Username: "alice", TotalTokens: 600, Share: 0.6},
				{Rank: 2, UserID: 2, Username: "bob", TotalTokens: 400, Share: 0.4},
			},
		},
		{
			name:        "empty totals yields empty slice",
			totals:      []model.RankingUserTotal{},
			totalTokens: 500,
			expected:    []RankedUser{},
		},
		{
			name: "zero total tokens yields zero share",
			totals: []model.RankingUserTotal{
				{UserID: 3, Username: "carol", TotalTokens: 100},
			},
			totalTokens: 0,
			expected: []RankedUser{
				{Rank: 1, UserID: 3, Username: "carol", TotalTokens: 100, Share: 0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := buildRankedUsers(tt.totals, tt.totalTokens)
			require.NotNil(t, rows)
			require.Len(t, rows, len(tt.expected))
			for i, want := range tt.expected {
				assert.Equal(t, want, rows[i])
			}
		})
	}
}
