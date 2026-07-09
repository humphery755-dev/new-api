package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// RestrictedWalletFunding extends wallet funding with restricted-quota awareness.
// It tries to consume from matching restricted quotas first, then falls back to
// the general wallet quota for any remaining amount.
type RestrictedWalletFunding struct {
	userId            int
	channelId         int
	walletConsumed    int // amount consumed from general wallet quota
	restrictedRecords []model.ConsumeRecord
}

func (r *RestrictedWalletFunding) Source() string { return BillingSourceWallet }

func (r *RestrictedWalletFunding) PreConsume(amount int) error {
	if amount <= 0 {
		return nil
	}

	// 1) Consume from matching restricted quotas first.
	consumed, records, err := model.ConsumeRestrictedQuotaByChannels(r.userId, r.channelId, amount)
	if err != nil {
		return err
	}
	r.restrictedRecords = records

	// 限定渠道匹配到了则只能用限定额度，不够就拒绝，不走钱包
	if consumed > 0 && consumed < amount {
		if rbErr := model.RollbackRestrictedQuota(records); rbErr != nil {
			_ = rbErr
		}
		return fmt.Errorf("限定额度不足: 需要 %s, 可用 %s", logger.LogQuota(amount), logger.LogQuota(consumed))
	}
	// 未匹配到限定额度 → 走钱包
	remaining := amount - consumed
	if remaining > 0 {
		userQuota, err := model.GetUserQuota(r.userId, false)
		if err != nil {
			if rbErr := model.RollbackRestrictedQuota(records); rbErr != nil {
				_ = rbErr
			}
			return err
		}
		if userQuota < remaining {
			if rbErr := model.RollbackRestrictedQuota(records); rbErr != nil {
				_ = rbErr
			}
			return fmt.Errorf("钱包余额不足: 剩余 %s, 需要 %s", logger.LogQuota(userQuota), logger.LogQuota(remaining))
		}
		if err := model.DecreaseUserQuota(r.userId, remaining, false); err != nil {
			if rbErr := model.RollbackRestrictedQuota(records); rbErr != nil {
				_ = rbErr
			}
			return err
		}
		r.walletConsumed = remaining
	}

	return nil
}

func (r *RestrictedWalletFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		// Additional charge: try restricted first, then wallet.
		extraConsumed, extraRecords, err := model.ConsumeRestrictedQuotaByChannels(r.userId, r.channelId, delta)
		if err != nil {
			return err
		}
		r.restrictedRecords = append(r.restrictedRecords, extraRecords...)
		remaining := delta - extraConsumed
		if remaining > 0 {
			if err := model.DecreaseUserQuota(r.userId, remaining, false); err != nil {
				return err
			}
			r.walletConsumed += remaining
		}
	} else {
		// Refund: return to wallet first, then restricted.
		refund := -delta
		if r.walletConsumed >= refund {
			if err := model.IncreaseUserQuota(r.userId, refund, false); err != nil {
				return err
			}
			r.walletConsumed -= refund
		} else {
			if r.walletConsumed > 0 {
				if err := model.IncreaseUserQuota(r.userId, r.walletConsumed, false); err != nil {
					return err
				}
				refund -= r.walletConsumed
				r.walletConsumed = 0
			}
			if refund > 0 && len(r.restrictedRecords) > 0 {
				if err := model.RollbackRestrictedQuota(r.restrictedRecords); err != nil {
					return err
				}
				r.restrictedRecords = nil
			}
		}
	}
	return nil
}

func (r *RestrictedWalletFunding) Refund() error {
	if r.walletConsumed > 0 {
		if err := model.IncreaseUserQuota(r.userId, r.walletConsumed, false); err != nil {
			return err
		}
		r.walletConsumed = 0
	}
	if len(r.restrictedRecords) > 0 {
		if err := model.RollbackRestrictedQuota(r.restrictedRecords); err != nil {
			return err
		}
		r.restrictedRecords = nil
	}
	return nil
}

// NewRestrictedWalletFunding creates the appropriate FundingSource for the request.
// If the user has active restricted quotas, returns *RestrictedWalletFunding;
// otherwise falls back to a plain *WalletFunding (zero overhead).
func NewRestrictedWalletFunding(relayInfo *relaycommon.RelayInfo) FundingSource {
	if relayInfo == nil {
		return &WalletFunding{userId: 0}
	}
	if model.HasUserRestrictedQuota(relayInfo.UserId) {
		return &RestrictedWalletFunding{
			userId:    relayInfo.UserId,
			channelId: relayInfo.ChannelId,
		}
	}
	return &WalletFunding{userId: relayInfo.UserId}
}
