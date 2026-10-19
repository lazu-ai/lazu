package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/logger"
	"gorm.io/gorm"
)

// UserIdentity stores a stable external identity independently from the user
// profile. A user can therefore sign in with more than one configured provider
// without one provider overwriting another provider's subject identifier.
type UserIdentity struct {
	ID        uint      `json:"-" gorm:"primaryKey"`
	UserID    int       `json:"-" gorm:"not null;uniqueIndex:idx_user_identity_user_provider"`
	Provider  string    `json:"-" gorm:"size:255;not null;uniqueIndex:idx_user_identity_user_provider;uniqueIndex:idx_user_identity_provider_subject"`
	Subject   string    `json:"-" gorm:"size:255;not null;uniqueIndex:idx_user_identity_provider_subject"`
	Email     string    `json:"-" gorm:"size:255;index"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
	User      User      `json:"-" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
}

// InsertWithIdentityTx creates an externally authenticated user and its first
// identity on the caller's transaction. OAuth resolution holds transaction-
// scoped advisory locks, so borrowing the package-global CoreDB() here would either
// escape those locks or deadlock a pool with a single open connection.
//
// Default API tokens, notifications, and cache work remain post-commit
// concerns. The security-critical account, identity, onboarding, and quota
// state is committed atomically here.
func (user *User) InsertWithIdentityTx(tx *gorm.DB, identity *UserIdentity) error {
	if tx == nil {
		return errors.New("database transaction is required")
	}
	if identity == nil || strings.TrimSpace(identity.Provider) == "" || strings.TrimSpace(identity.Subject) == "" {
		return errors.New("external identity is incomplete")
	}
	return user.insertAccountTx(tx, identity)
}

// InsertTx creates a password-authenticated account on the caller's
// transaction. The registration controller uses this while holding the same
// normalized-email advisory lock as OAuth/OIDC account creation.
func (user *User) InsertTx(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("database transaction is required")
	}
	return user.insertAccountTx(tx, nil)
}

func (user *User) insertAccountTx(tx *gorm.DB, identity *UserIdentity) error {
	var err error
	if user.Password != "" {
		user.Password, err = common.Password2Hash(user.Password)
		if err != nil {
			return err
		}
	}

	signupQuota := common.QuotaForNewUser
	user.Quota = 0
	setting := user.GetSetting()
	setting.OnboardingActive = true
	if sidebar := generateDefaultSidebarConfigForRole(user.Role); sidebar != "" {
		setting.SidebarModules = sidebar
	}
	user.SetSetting(setting)

	if err := tx.Create(user).Error; err != nil {
		return err
	}
	if identity != nil {
		identity.UserID = user.Id
		if err := tx.Create(identity).Error; err != nil {
			return err
		}
	}
	if signupQuota > 0 {
		if err := adjustUserQuotaWithLedgerTx(tx, AdjustUserQuotaWithLedgerInput{
			UserID:         user.Id,
			DeltaQuota:     signupQuota,
			Kind:           WalletLedgerKindBonus,
			RefType:        "user_signup",
			RefID:          fmt.Sprintf("user:%d:signup", user.Id),
			PricingVersion: defaultPricingSeedVersion,
			Metadata: map[string]any{
				"quota": signupQuota,
			},
		}); err != nil {
			return err
		}
	}
	user.Quota = signupQuota
	return nil
}

// RecordNewUserSignupEffects preserves the best-effort audit trail emitted by
// the historical User.Insert path. It must run only after the caller's
// transaction commits; TelemetryDB() is intentionally independent from the primary
// user transaction.
func RecordNewUserSignupEffects(user *User) {
	if user == nil || user.Id <= 0 {
		return
	}
	common.SysLog(fmt.Sprintf("Initializing sidebar configuration for new user %s (role: %d)", user.Username, user.Role))
	if common.QuotaForNewUser > 0 {
		RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("新用户注册赠送 %s", logger.LogQuota(common.QuotaForNewUser)))
	}
}
