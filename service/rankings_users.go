package service

import (
	"github.com/QuantumNous/new-api/model"
)

const rankingUserLeaderboardLimit = 20

type RankedUser struct {
	Rank        int     `json:"rank"`
	UserID      int     `json:"user_id"`
	Username    string  `json:"username"`
	TotalTokens int64   `json:"total_tokens"`
	Share       float64 `json:"share"`
}

func buildRankedUsers(totals []model.RankingUserTotal, totalTokens int64) []RankedUser {
	rows := make([]RankedUser, 0, len(totals))
	for idx, item := range totals {
		rows = append(rows, RankedUser{
			Rank:        idx + 1,
			UserID:      item.UserID,
			Username:    item.Username,
			TotalTokens: item.TotalTokens,
			Share:       rankingShare(item.TotalTokens, totalTokens),
		})
	}
	return rows
}
