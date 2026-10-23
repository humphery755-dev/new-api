package model

type RankingUserTotal struct {
	UserID      int    `json:"user_id"`
	Username    string `json:"username"`
	TotalTokens int64  `json:"total_tokens"`
}

const defaultRankingUserLimit = 20

func GetRankingUserTotals(startTime int64, endTime int64, limit int) ([]RankingUserTotal, error) {
	if limit <= 0 {
		limit = defaultRankingUserLimit
	}
	usernameExpr := "coalesce(nullif(users.username, ''), quota_data.username)"
	var rows []RankingUserTotal
	query := DB.Table("quota_data").
		Select("quota_data.user_id as user_id, " + usernameExpr + " as username, sum(quota_data.token_used) as total_tokens").
		Joins("LEFT JOIN users ON users.id = quota_data.user_id").
		Where("quota_data.username <> ''").
		Group("quota_data.user_id, " + usernameExpr).
		Having("sum(quota_data.token_used) > 0").
		Order("total_tokens DESC, quota_data.user_id ASC").
		Limit(limit)
	if startTime > 0 {
		query = query.Where("quota_data.created_at >= ?", startTime)
	}
	if endTime > 0 {
		query = query.Where("quota_data.created_at <= ?", endTime)
	}
	err := query.Find(&rows).Error
	return rows, err
}
