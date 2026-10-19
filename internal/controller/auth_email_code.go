package controller

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// One code, both doors.
//
// Asking someone to choose between "sign in" and "sign up" before they have
// typed anything is a question only the system can answer, and getting it
// wrong is what produced the OAuth dead end this flow replaces. Here the
// address is the whole question: a code goes to the mailbox either way, and
// whoever proves they hold it either lands in their account or gets one.
//
// Every response on the start path is identical. Whether an address is
// registered is told only to the mailbox owner.

var (
	errEmailAuthPolicyDomain = types.Err(types.ErrorCodeEmailDomainNotAllowed)
	errEmailAuthPolicyAlias  = types.Err(types.ErrorCodeEmailAliasNotAllowed)
	errEmailAuthDisabled     = types.Err(types.ErrorCodeEmailCodeAuthDisabled)
	errEmailTransportMissing = types.Err(types.ErrorCodeEmailTransportUnavailable)
	errEmailInvalid          = types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "email"})
)

type emailAuthStartRequest struct {
	Email string `json:"email"`
	// Invite is a project invitation token. It lets the address it names
	// sign up while registration is otherwise closed.
	Invite string `json:"invite,omitempty"`
}

type emailAuthVerifyRequest struct {
	Code   string `json:"code"`
	Email  string `json:"email"`
	Invite string `json:"invite,omitempty"`
}

// validateEmailForAuth applies the deployment's address policy. It runs before
// anything is sent, and its failures are the only ones the start path reports —
// they are properties of the submitted address, not of our user table.
func validateEmailForAuth(email string) error {
	if err := common.Validate.Var(email, "required,email"); err != nil {
		return errEmailInvalid
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return errEmailInvalid
	}
	localPart, domainPart := parts[0], parts[1]
	if common.EmailDomainRestrictionEnabled {
		allowed := false
		for _, domain := range common.EmailDomainWhitelist {
			if domainPart == domain {
				allowed = true
				break
			}
		}
		if !allowed {
			return errEmailAuthPolicyDomain
		}
	}
	if common.EmailAliasRestrictionEnabled {
		if strings.Contains(localPart, "+") || strings.Contains(localPart, ".") {
			return errEmailAuthPolicyAlias
		}
	}
	return nil
}

// StartEmailAuth mails a sign-in code to the address, creating nothing yet.
//
// The reply says only that the request was accepted. Account lookup and mail
// delivery both happen off the request path, because answering faster for an
// unknown address than for a known one is itself an enumeration oracle.
func StartEmailAuth(c *gin.Context) {
	if !common.EmailCodeAuthEnabled {
		httperr.Write(c, errEmailAuthDisabled)
		return
	}
	if !common.EmailTransportConfigured() {
		httperr.Write(c, errEmailTransportMissing)
		return
	}
	var request emailAuthStartRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}
	email := strings.ToLower(strings.TrimSpace(request.Email))
	if err := validateEmailForAuth(email); err != nil {
		httperr.Write(c, err)
		return
	}
	if !allowMailTo(c, email) {
		return
	}
	lang := requestMailLang(c)
	invited := model.ProjectInviteOpensSignup(strings.TrimSpace(request.Invite), email, time.Now())
	enqueueAuthEmailJob("auth-email-code-request", func() {
		dispatchEmailAuthCode(email, lang, invited)
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func dispatchEmailAuthCode(email string, lang string, invited bool) {
	// A closed deployment still answers the mailbox owner, and only them, so an
	// unregistered address learns registration is closed instead of waiting for
	// a code that will never arrive. An invited address may still sign up.
	if !common.RegisterEnabled && !invited && !model.IsEmailAlreadyTaken(email) {
		subject, body := authRegistrationClosedMail(lang)
		sendAuthEmail(subject, email, body)
		return
	}
	code, err := common.GenerateNumericVerificationCode(6)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to generate email auth code: %v", err))
		return
	}
	common.RegisterVerificationCodeWithKey(email, code, common.EmailAuthPurpose)
	// A newly issued code restarts the guess allowance; otherwise a burned-out
	// counter would keep rejecting codes the person legitimately requested.
	common.ClearVerificationFailures(email, common.EmailAuthPurpose)
	subject, body := authLoginCodeMail(lang, code)
	sendAuthEmail(subject, email, body)
}

// VerifyEmailAuth consumes the code and hands back a session, creating the
// account first if the address has never been seen.
func VerifyEmailAuth(c *gin.Context) {
	if !common.EmailCodeAuthEnabled {
		httperr.Write(c, errEmailAuthDisabled)
		return
	}
	var request emailAuthVerifyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}
	email := strings.ToLower(strings.TrimSpace(request.Email))
	code := strings.TrimSpace(request.Code)
	if err := validateEmailForAuth(email); err != nil {
		httperr.Write(c, err)
		return
	}
	if code == "" {
		httperr.Code(c, types.ErrorCodeVerificationCodeInvalid)
		return
	}
	if !common.VerifyCodeWithKey(email, code, common.EmailAuthPurpose) {
		if common.RecordVerificationFailure(email, common.EmailAuthPurpose) {
			httperr.Code(c, types.ErrorCodeVerificationCodeAttemptsExhausted)
			return
		}
		httperr.Code(c, types.ErrorCodeVerificationCodeInvalid)
		return
	}
	common.ClearVerificationFailures(email, common.EmailAuthPurpose)

	invited := model.ProjectInviteOpensSignup(strings.TrimSpace(request.Invite), email, time.Now())
	user, created, err := resolveEmailAuthUserFor(email, invited)
	if err != nil {
		// Catalog errors (a closed registration, an ambiguous address) reach the
		// person; anything else is logged with the request ID and answered as
		// internal_error.
		httperr.Write(c, err)
		return
	}
	if user.Status != common.UserStatusEnabled {
		httperr.Code(c, types.ErrorCodeUserBanned)
		return
	}
	if created {
		SendWelcomeEmail(user, emailAuthProviderLabel(), userMailLang(user, c), requestHomeSurface(c))
	}
	setupLogin(user, c)
}

func emailAuthProviderLabel() string {
	return "邮箱验证码"
}

// resolveEmailAuthUser finds or creates the account behind a proven address.
// It takes the same verified-email lock every other external-auth resolver
// takes, so a first email-code sign-in and a first OAuth callback for one
// mailbox cannot both decide there is no owner and commit two accounts.
func resolveEmailAuthUser(email string, _ ...string) (*model.User, bool, error) {
	return resolveEmailAuthUserFor(email, false)
}

// resolveEmailAuthUserFor is resolveEmailAuthUser with the one exception to a
// closed registration: an address a live project invitation was sent to.
func resolveEmailAuthUserFor(email string, invited bool) (*model.User, bool, error) {
	if !common.EmailCodeAuthEnabled {
		return nil, false, errEmailAuthDisabled
	}
	var user *model.User
	created := false
	err := withAccountResolutionLocks(model.CoreDB(), []string{
		verifiedEmailOwnershipLockKey(email),
		externalUsernameAllocationLockKey(usernameFromEmailLocal(email)),
	}, func(tx *gorm.DB) error {
		existing, findErr := findUserByVerifiedEmail(tx, email)
		if findErr != nil {
			return findErr
		}
		if existing != nil {
			user = existing
			return nil
		}
		if !common.RegisterEnabled && !invited {
			return errOAuthRegistrationClosed
		}
		var createErr error
		user, createErr = createEmailAuthUserTx(tx, email)
		if createErr != nil {
			return createErr
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if created {
		model.RecordNewUserSignupEffects(user)
		if err := initializeNewUserFeatures(user); err != nil {
			return nil, false, err
		}
	}
	if err := model.InvalidateUserCache(user.Id); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate email auth user cache: %v", err))
	}
	return user, created, nil
}

// usernameFromEmailLocal derives the handle to reserve from the local part, so
// the person never has to invent one at the door.
func usernameFromEmailLocal(email string) string {
	local := email
	if index := strings.Index(email, "@"); index > 0 {
		local = email[:index]
	}
	return sanitizeOIDCUsername(local)
}

func createEmailAuthUserTx(db *gorm.DB, email string) (*model.User, error) {
	// The row still needs a password column value; a random one that nobody
	// holds keeps password sign-in closed until the person sets a real one.
	password, err := randomOIDCString()
	if err != nil {
		return nil, err
	}
	username, err := uniqueOIDCUsernameTx(db, usernameFromEmailLocal(email))
	if err != nil {
		return nil, err
	}
	user := &model.User{
		Username:      username,
		DisplayName:   truncateDisplayName(username),
		Password:      password,
		Email:         email,
		VerifiedEmail: email,
		Role:          common.RoleCommonUser,
		Status:        common.UserStatusEnabled,
		TermsVersion:  common.TermsVersion,
		TermsAgreedAt: time.Now().Unix(),
	}
	setting := user.GetSetting()
	setting.NeedsPassword = true
	user.SetSetting(setting)
	if err := user.InsertTx(db); err != nil {
		return nil, err
	}
	return user, nil
}
