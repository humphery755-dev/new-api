package model

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

// UserRestrictedQuota stores quota that is restricted to specific channels.
// Admin-gifted quota can be limited to certain channels; other channels must use general quota.
type UserRestrictedQuota struct {
	Id             int64  `json:"id"`
	UserId         int    `json:"user_id" gorm:"index"`
	Quota          int    `json:"quota"`
	UsedQuota      int    `json:"used_quota"`
	AllowedChannels string `json:"allowed_channels" gorm:"type:text"` // JSON array of channel IDs, e.g. "[1,2,3]"
	CreatedAt      int64  `json:"created_at"`
	Remark         string `json:"remark"`
}

// ConsumeRecord tracks a single restricted quota consumption for rollback.
type ConsumeRecord struct {
	Id     int64 `json:"id"`
	Amount int   `json:"amount"`
}

// CreateUserRestrictedQuota creates a restricted quota entry for the given user.
func CreateUserRestrictedQuota(userId int, quota int, channels []int, remark string) error {
	if quota <= 0 {
		return fmt.Errorf("quota must be positive")
	}
	channelsJSON, err := common.Marshal(channels)
	if err != nil {
		return fmt.Errorf("failed to marshal channels: %w", err)
	}

	entry := &UserRestrictedQuota{
		UserId:         userId,
		Quota:          quota,
		UsedQuota:      0,
		AllowedChannels: string(channelsJSON),
		CreatedAt:      common.GetTimestamp(),
		Remark:         remark,
	}

	if err := DB.Create(entry).Error; err != nil {
		return err
	}

	RecordLog(userId, LogTypeRestrictedQuota, fmt.Sprintf("管理员赠送限定额度 %s，限定渠道: %s",
		logger.LogQuota(quota), formatChannelList(channels)))
	return nil
}

// ConsumeRestrictedQuotaByChannels consumes quota from matching restricted entries.
// Returns the total consumed amount, per-entry consumption records, and any error.
func ConsumeRestrictedQuotaByChannels(userId int, channelId int, amount int) (int, []ConsumeRecord, error) {
	if amount <= 0 {
		return 0, nil, nil
	}

	var entries []UserRestrictedQuota
	err := DB.Where("user_id = ? AND quota > 0", userId).Order("id ASC").Find(&entries).Error
	if err != nil {
		return 0, nil, err
	}

	var matching []UserRestrictedQuota
	for _, e := range entries {
		var channels []int
		if err := common.Unmarshal([]byte(e.AllowedChannels), &channels); err != nil {
			continue
		}
		for _, ch := range channels {
			if ch == channelId {
				matching = append(matching, e)
				break
			}
		}
	}

	if len(matching) == 0 {
		return 0, nil, nil
	}

	// Build cross-DB-safe column reference for `id` (which is not a reserved word,
	// but quoting is kept consistent with other model functions that do the same).
	refCol := "`id`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"id"`
	}

	var consumed int
	var records []ConsumeRecord

	err = DB.Transaction(func(tx *gorm.DB) error {
		for _, e := range matching {
			if consumed >= amount {
				break
			}
			need := amount - consumed
			if e.Quota < need {
				need = e.Quota
			}

			result := tx.Model(&UserRestrictedQuota{}).
				Where(refCol+" = ? AND quota >= ?", e.Id, need).
				Updates(map[string]interface{}{
					"quota":      gorm.Expr("quota - ?", need),
					"used_quota": gorm.Expr("used_quota + ?", need),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				continue
			}

			consumed += need
			records = append(records, ConsumeRecord{Id: e.Id, Amount: need})
		}
		return nil
	})

	if err != nil {
		return 0, nil, err
	}

	return consumed, records, nil
}

// RollbackRestrictedQuota rolls back a previous consumption in a single transaction.
func RollbackRestrictedQuota(records []ConsumeRecord) error {
	if len(records) == 0 {
		return nil
	}

	refCol := "`id`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"id"`
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		for _, r := range records {
			if err := tx.Model(&UserRestrictedQuota{}).
				Where(refCol+" = ?", r.Id).
				Updates(map[string]interface{}{
					"quota":      gorm.Expr("quota + ?", r.Amount),
					"used_quota": gorm.Expr("used_quota - ?", r.Amount),
				}).Error; err != nil {
				return fmt.Errorf("rollback restricted quota %d: %w", r.Id, err)
			}
		}
		return nil
	})
}

// GetUserRestrictedQuotas returns all restricted quotas for a user (for admin display).
func GetUserRestrictedQuotas(userId int) ([]UserRestrictedQuota, error) {
	var entries []UserRestrictedQuota
	err := DB.Where("user_id = ?", userId).Order("id DESC").Find(&entries).Error
	return entries, err
}

// HasUserRestrictedQuota checks if a user has any active restricted quota.
func HasUserRestrictedQuota(userId int) bool {
	if DB == nil {
		return false
	}
	var count int64
	DB.Model(&UserRestrictedQuota{}).Where("user_id = ? AND quota > 0", userId).Count(&count)
	return count > 0
}

// GetUserRemainingRestrictedQuota returns the total remaining restricted quota for a user.
func GetUserRemainingRestrictedQuota(userId int) int {
	if DB == nil {
		return 0
	}
	var total int
	DB.Model(&UserRestrictedQuota{}).
		Where("user_id = ? AND quota > 0", userId).
		Select("COALESCE(SUM(quota), 0)").
		Scan(&total)
	return total
}

// RestrictedQuotaSummary holds aggregated restricted quota info for a user.
type RestrictedQuotaSummary struct {
	Quota    int    // total remaining restricted quota
	Channels string // JSON array of unique channel IDs, e.g. "[1,2,3]"
}

// GetUsersRestrictedQuotaSummaryMap returns a map of userId -> RestrictedQuotaSummary
// for batch enrichment of user list responses.
func GetUsersRestrictedQuotaSummaryMap(userIds []int) map[int]RestrictedQuotaSummary {
	if DB == nil || len(userIds) == 0 {
		return nil
	}

	var entries []UserRestrictedQuota
	if err := DB.Where("user_id IN ? AND quota > 0", userIds).Order("id ASC").Find(&entries).Error; err != nil {
		return nil
	}

	type accum struct {
		quota    int
		channels map[int]bool
	}
	acc := make(map[int]*accum)

	for _, e := range entries {
		a, ok := acc[e.UserId]
		if !ok {
			a = &accum{channels: make(map[int]bool)}
			acc[e.UserId] = a
		}
		a.quota += e.Quota

		var channels []int
		if err := common.Unmarshal([]byte(e.AllowedChannels), &channels); err == nil {
			for _, ch := range channels {
				a.channels[ch] = true
			}
		}
	}

	result := make(map[int]RestrictedQuotaSummary, len(acc))
	for userId, a := range acc {
		summary := RestrictedQuotaSummary{Quota: a.quota}
		if len(a.channels) > 0 {
			chList := make([]int, 0, len(a.channels))
			for ch := range a.channels {
				chList = append(chList, ch)
			}
			chJSON, err := common.Marshal(chList)
			if err == nil {
				summary.Channels = string(chJSON)
			}
		}
		result[userId] = summary
	}

	return result
}

func formatChannelList(channels []int) string {
	strs := make([]string, len(channels))
	for i, ch := range channels {
		strs[i] = fmt.Sprintf("%d", ch)
	}
	return "[" + strings.Join(strs, ",") + "]"
}
