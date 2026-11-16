package controller

import (
	"fmt"
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const emailBindingPurpose = "email-binding"
const emailBindingCurrentPurpose = "email-binding-current"

type emailBindingRequest struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	CurrentCode string `json:"current_code"`
}

// Binding is a session-only identity operation. Management credentials cannot
// change the mailbox used for account recovery.
func emailBindingUser(c *gin.Context) (*model.User, error) {
	id := c.GetInt("id")
	session := sessions.Default(c)
	if id <= 0 || c.GetBool("use_access_token") || common.ParseIntFromAny(session.Get("id")) != id {
		return nil, types.Err(types.ErrorCodeSessionRequired)
	}
	var user model.User
	if err := model.CoreDB().First(&user, id).Error; err != nil {
		return nil, err
	}
	if session.Get("session_version") == nil || user.Status != common.UserStatusEnabled || common.ParseIntFromAny(session.Get("session_version")) != user.SessionVersion {
		return nil, types.Err(types.ErrorCodeSessionInvalid)
	}
	return &user, nil
}

func emailBindingKey(user *model.User, email string) string {
	// Changing a password, revoking sessions, or changing either address makes
	// every outstanding challenge unusable, including an asynchronously sent one.
	return fmt.Sprintf("%d:%d:%s", user.Id, user.SessionVersion, common.GenerateHMAC(user.Password+":"+strings.ToLower(strings.TrimSpace(user.Email))+":"+email))
}

func emailBindingNeedsCurrent(user *model.User, email string) bool {
	return strings.TrimSpace(user.Email) != "" && !strings.EqualFold(strings.TrimSpace(user.Email), email)
}

func readEmailBindingRequest(c *gin.Context) (*emailBindingRequest, bool) {
	var request emailBindingRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httperr.Invalid(c, "email")
		return nil, false
	}
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	if err := validateEmailForAuth(request.Email); err != nil {
		httperr.Write(c, err)
		return nil, false
	}
	return &request, true
}

func StartEmailBinding(c *gin.Context) {
	request, ok := readEmailBindingRequest(c)
	if !ok {
		return
	}
	user, err := emailBindingUser(c)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if !common.EmailTransportConfigured() {
		httperr.Write(c, errEmailTransportMissing)
		return
	}
	if !allowMailTo(c, request.Email) {
		return
	}
	needsCurrent := emailBindingNeedsCurrent(user, request.Email)
	if needsCurrent && !allowMailTo(c, user.Email) {
		return
	}
	lang := requestMailLang(c)
	enqueueAuthEmailJob("email-binding-request", func() {
		key := emailBindingKey(user, request.Email)
		sendEmailBindingCode(key, request.Email, request.Email, emailBindingPurpose, lang)
		if needsCurrent {
			sendEmailBindingCode(key, user.Email, request.Email, emailBindingCurrentPurpose, lang)
		}
	})
	// No target-owner lookup in this response: it must not enumerate accounts.
	common.ApiSuccess(c, gin.H{"current_email_required": needsCurrent})
}

func sendEmailBindingCode(key, email, target, purpose, lang string) {
	code, err := common.GenerateNumericVerificationCode(6)
	if err != nil {
		common.SysError("failed to generate email binding code")
		return
	}
	common.RegisterVerificationCodeWithKey(key, code, purpose)
	common.ClearVerificationFailures(key, purpose)
	subject, body := authEmailBindingMail(lang, code, target)
	sendAuthEmail(subject, email, body)
}

func consumeEmailBindingCode(key, code, purpose string) bool {
	if !common.VerifyCodeWithKey(key, strings.TrimSpace(code), purpose) {
		common.RecordVerificationFailure(key, purpose)
		return false
	}
	common.ClearVerificationFailures(key, purpose)
	return true
}

func EmailBind(c *gin.Context) {
	request, ok := readEmailBindingRequest(c)
	if !ok {
		return
	}
	user, err := emailBindingUser(c)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	err = bindEmail(user, request)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := model.InvalidateUserCache(user.Id); err != nil {
		common.SysWarn(fmt.Sprintf("email binding cache invalidation: %v", err))
	}
	common.ApiSuccess(c, nil)
}

func bindEmail(user *model.User, request *emailBindingRequest) error {
	// Share both address locks with email/OAuth signup, and lock the user row
	// against password resets and concurrent bindings before consuming proofs.
	return withAccountResolutionLocks(model.CoreDB(), []string{
		verifiedEmailOwnershipLockKey(user.Email), verifiedEmailOwnershipLockKey(request.Email),
	}, func(tx *gorm.DB) error {
		var current model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, user.Id).Error; err != nil {
			return err
		}
		key := emailBindingKey(&current, request.Email)
		if current.Status != common.UserStatusEnabled || key != emailBindingKey(user, request.Email) {
			return types.Err(types.ErrorCodeSessionInvalid)
		}
		// Consume the new-address proof first; neither code can be reused to probe
		// other accounts or change a different target mailbox.
		if !consumeEmailBindingCode(key, request.Code, emailBindingPurpose) {
			return types.Err(types.ErrorCodeVerificationCodeInvalid)
		}
		if emailBindingNeedsCurrent(&current, request.Email) && !consumeEmailBindingCode(key, request.CurrentCode, emailBindingCurrentPurpose) {
			return types.Err(types.ErrorCodeVerificationCodeInvalid)
		}
		var owners int64
		if err := tx.Unscoped().Model(&model.User{}).Where("id <> ? AND (LOWER(TRIM(email)) = ? OR LOWER(TRIM(verified_email)) = ?)", current.Id, request.Email, request.Email).Count(&owners).Error; err != nil {
			return err
		}
		if owners != 0 {
			return errOAuthEmailConflict
		}
		return tx.Model(&current).Updates(map[string]interface{}{"email": request.Email, "verified_email": request.Email}).Error
	})
}
