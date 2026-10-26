package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/securecookie"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	oidcTransactionCookie = "lazu_oidc_tx"
	oidcTransactionMaxAge = 10 * 60
)

type oidcTransaction struct {
	State        string `json:"state"`
	Nonce        string `json:"nonce"`
	CodeVerifier string `json:"code_verifier"`
	Next         string `json:"next"`
}

const oidcIdentityProvider = "oidc"

var errOIDCUserNotFound = errors.New("oidc user not found")

func validOIDCTransaction(tx oidcTransaction, callbackState string) bool {
	return tx.State != "" &&
		tx.Nonce != "" &&
		tx.CodeVerifier != "" &&
		callbackState == tx.State
}

// oidcProvider names the configured identity provider when we recognise it, so
// the sign-in button can carry that provider's own mark instead of a generic
// one. An unrecognised issuer returns "", and the UI falls back to plain SSO.
func oidcProvider() string {
	host := strings.ToLower(strings.TrimSpace(common.OIDCIssuerURL))
	if host == "" {
		return ""
	}
	if parsed, err := url.Parse(host); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	host = strings.TrimSuffix(host, ".")
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	// Match provider-owned hosts on domain boundaries only. A substring
	// check would let github.evil.example wear the GitHub mark while the
	// redirect goes somewhere else entirely — worse than the neutral
	// fallback. Self-hosted GitLab instances land on the neutral button by
	// design: we cannot vouch for a host we do not recognize.
	switch {
	case hostIs(host, "google.com") || hostIs(host, "googleapis.com"):
		return "google"
	case hostIs(host, "gitlab.com"):
		return "gitlab"
	case hostIs(host, "microsoftonline.com") || hostIs(host, "login.live.com"):
		return "microsoft"
	default:
		return ""
	}
}

// hostIs reports whether host equals domain or is a subdomain of it.
func hostIs(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func OIDCStatus(c *gin.Context) {
	providers := configuredSocialAuthProviders()
	if oidcConfigured() {
		providers = append(providers, authProviderStatus{ID: "oidc", Provider: oidcProvider()})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			// Keep the legacy field scoped to the legacy OIDC route. New clients
			// use providers, which can contain Google, GitHub, and OIDC together.
			"enabled":         oidcConfigured(),
			"password_login":  common.PasswordLoginEnabled,
			"email_code_auth": common.EmailCodeAuthEnabled && common.EmailTransportConfigured(),
			"break_glass":     common.OIDCBreakGlassPasswordEnabled,
			"provider":        oidcProvider(),
			"providers":       providers,
		},
	})
}

func OIDCLogin(c *gin.Context) {
	next := sanitizedOIDCNext(c.Query("next"))
	oauthConfig, _, err := oidcOAuthConfig(c.Request.Context(), c)
	if err != nil {
		common.SysLog(fmt.Sprintf("oidc login rejected: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthProviderNotConfigured, next)
		return
	}
	state, err := randomOIDCString()
	if err != nil {
		common.SysLog(fmt.Sprintf("oidc state generation failed: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthLoginFailed, next)
		return
	}
	nonce, err := randomOIDCString()
	if err != nil {
		common.SysLog(fmt.Sprintf("oidc nonce generation failed: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthLoginFailed, next)
		return
	}
	verifier := oauth2.GenerateVerifier()
	if err := setOIDCTransaction(c, oidcTransaction{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: verifier,
		Next:         next,
	}); err != nil {
		common.SysLog(fmt.Sprintf("oidc transaction cookie failed: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthLoginFailed, next)
		return
	}
	c.Redirect(http.StatusFound, oauthConfig.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)))
}

func OIDCCallback(c *gin.Context) {
	// Read the transaction first so a cancelled or stale sign-in still
	// returns to the door it started from.
	tx, err := readOIDCTransaction(c)
	if errMessage := c.Query("error"); errMessage != "" {
		clearOIDCTransaction(c)
		common.SysLog(fmt.Sprintf("oidc provider returned error: %s", errMessage))
		oauthLoginErrorRedirectNext(c, oauthProviderErrorCode(errMessage), tx.Next)
		return
	}
	if err != nil || !validOIDCTransaction(tx, c.Query("state")) {
		clearOIDCTransaction(c)
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthStateInvalid, tx.Next)
		return
	}
	// Consume state/nonce/PKCE material before the one-time code exchange.
	clearOIDCTransaction(c)
	oauthConfig, verifier, err := oidcOAuthConfig(c.Request.Context(), c)
	if err != nil {
		common.SysLog(fmt.Sprintf("oidc callback rejected: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthProviderNotConfigured, tx.Next)
		return
	}
	token, err := oauthConfig.Exchange(c.Request.Context(), c.Query("code"), oauth2.VerifierOption(tx.CodeVerifier))
	if err != nil {
		common.SysLog(fmt.Sprintf("oidc token exchange failed: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthExchangeFailed, tx.Next)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		common.SysLog("oidc id_token missing from token response")
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthIdentityInvalid, tx.Next)
		return
	}
	idToken, err := verifier.Verify(c.Request.Context(), rawIDToken)
	if err != nil {
		common.SysLog(fmt.Sprintf("oidc id_token verification failed: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthIdentityInvalid, tx.Next)
		return
	}
	if idToken.Nonce != tx.Nonce {
		common.SysLog("oidc nonce mismatch")
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthIdentityInvalid, tx.Next)
		return
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		common.SysLog(fmt.Sprintf("oidc claims parse failed: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthIdentityInvalid, tx.Next)
		return
	}
	resolveCtx := withMailSurface(withMailLang(c.Request.Context(), requestMailLang(c)), requestHomeSurface(c))
	user, err := resolveOIDCUser(resolveCtx, idToken.Subject, claims)
	if err != nil {
		common.SysLog(fmt.Sprintf("oidc user resolution failed: %v", err))
		oauthLoginErrorRedirectNext(c, oauthRedirectCode(err), tx.Next)
		return
	}
	if user.Status != common.UserStatusEnabled {
		oauthLoginErrorRedirectNext(c, types.ErrorCodeUserBanned, tx.Next)
		return
	}
	if _, err := setupSession(user, c); err != nil {
		common.SysLog(fmt.Sprintf("oidc session setup failed: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthLoginFailed, tx.Next)
		return
	}
	next := sanitizedOIDCNext(tx.Next)
	c.Redirect(http.StatusFound, oidcFrontendCallbackURL(next))
}

func OIDCBootstrap(c *gin.Context) {
	session := sessions.Default(c)
	userID, ok := sessionUserID(session.Get("id"))
	if !ok || userID <= 0 {
		httperr.Code(c, types.ErrorCodeSessionRequired)
		return
	}
	user, err := model.GetUserById(userID, false)
	if err != nil {
		session.Clear()
		_ = session.Save()
		httperr.Code(c, types.ErrorCodeSessionExpired)
		return
	}
	sessionVersion, ok := sessionUserID(session.Get("session_version"))
	if !ok || sessionVersion != user.SessionVersion {
		session.Clear()
		_ = session.Save()
		httperr.Code(c, types.ErrorCodeSessionExpired)
		return
	}
	if user.Status != common.UserStatusEnabled {
		httperr.Code(c, types.ErrorCodeUserBanned)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    cleanUserForLogin(user),
	})
}

func oidcOAuthConfig(ctx context.Context, c *gin.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	if !oidcConfigured() {
		return nil, nil, errors.New("oidc is not configured")
	}
	provider, err := oidc.NewProvider(ctx, strings.TrimRight(common.OIDCIssuerURL, "/"))
	if err != nil {
		return nil, nil, err
	}
	redirectURL := strings.TrimSpace(common.OIDCRedirectURL)
	if redirectURL == "" && c != nil && c.Request != nil {
		baseURL, err := requestBaseURL(c)
		if err != nil {
			return nil, nil, err
		}
		redirectURL = baseURL + "/api/auth/oidc/callback"
	}
	if redirectURL == "" {
		return nil, nil, errors.New("oidc redirect url is required")
	}
	oauthConfig := &oauth2.Config{
		ClientID:     common.OIDCClientID,
		ClientSecret: common.OIDCClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  redirectURL,
		Scopes:       oidcScopes(),
	}
	return oauthConfig, provider.Verifier(&oidc.Config{ClientID: common.OIDCClientID}), nil
}

func oidcConfigured() bool {
	return common.AuthProviderEnabled(oidcIdentityProvider)
}

func oidcScopes() []string {
	fields := strings.FieldsFunc(common.OIDCScopes, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	})
	if len(fields) == 0 {
		return []string{oidc.ScopeOpenID, "email", "profile"}
	}
	return fields
}

func resolveOIDCUser(ctx context.Context, subject string, claims map[string]any) (*model.User, error) {
	if model.CoreDB() == nil {
		return nil, errors.New("database is not initialized")
	}
	issuer := canonicalOIDCIssuer(common.OIDCIssuerURL)
	subject = strings.TrimSpace(subject)
	if issuer == "" || subject == "" {
		return nil, errors.New("oidc identity is incomplete")
	}
	claimedEmail := claimString(claims, common.OIDCEmailClaim)
	verifiedEmail := ""
	if claimBool(claims, "email_verified") {
		verifiedEmail = strings.TrimSpace(claimedEmail)
	}
	username := oidcUsername(claims, claimedEmail)
	displayName := claimString(claims, "name")
	if displayName == "" {
		displayName = username
	}
	groups := claimStringSlice(claims, common.OIDCRoleClaim)
	identitySubject := oidcNamespacedSubject(issuer, subject)
	lockEmail := strings.ToLower(verifiedEmail)
	if lockEmail == "" {
		// Providers are allowed to omit email. Use the immutable identity as the
		// second lock key rather than serializing every email-less OIDC callback.
		lockEmail = identitySubject
	}

	var user *model.User
	created := false
	err := withSocialOAuthResolutionLock(model.CoreDB().WithContext(ctx), socialIdentity{
		Provider: oidcIdentityProvider,
		Subject:  identitySubject,
		Email:    lockEmail,
		Username: username,
	}, func(tx *gorm.DB) error {
		var resolveErr error
		user, created, resolveErr = resolveOIDCUserLocked(
			tx,
			issuer,
			subject,
			identitySubject,
			username,
			verifiedEmail,
			displayName,
			groups,
		)
		return resolveErr
	})
	if err != nil {
		return nil, err
	}
	if created {
		model.RecordNewUserSignupEffects(user)
		if err := initializeNewUserFeatures(user); err != nil {
			return nil, err
		}
		SendWelcomeEmail(user, "OIDC", mailLangFromContext(ctx), mailSurfaceFromContext(ctx))
	}
	if err := model.InvalidateUserCache(user.Id); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate oidc user cache: %v", err))
	}
	if err := model.InvalidateUserTokensCache(user.Id); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate oidc user token cache: %v", err))
	}
	return user, nil
}

func resolveOIDCUserLocked(
	db *gorm.DB,
	issuer string,
	subject string,
	identitySubject string,
	username string,
	verifiedEmail string,
	displayName string,
	groups []string,
) (*model.User, bool, error) {
	user, err := updateExistingOIDCUser(
		db, issuer, subject, identitySubject, verifiedEmail, displayName, groups,
	)
	if err == nil {
		return user, false, nil
	}
	if !errors.Is(err, errOIDCUserNotFound) {
		return nil, false, err
	}
	if !common.OIDCAutoCreateUsers {
		return nil, false, errOAuthRegistrationClosed
	}
	role := oidcMappedRole(groups, nil)
	user, err = createOIDCUserTx(db, issuer, subject, identitySubject, username, verifiedEmail, displayName, role)
	return user, err == nil, err
}

func updateExistingOIDCUser(
	db *gorm.DB,
	issuer string,
	subject string,
	identitySubject string,
	verifiedEmail string,
	displayName string,
	groups []string,
) (*model.User, error) {
	var updated model.User
	candidate, err := findExistingOIDCUserTx(db, issuer, subject, verifiedEmail)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, errOIDCUserNotFound
	}
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&updated, candidate.Id).Error; err != nil {
		return nil, err
	}
	if err := ensureOIDCEmailAvailableTx(db, verifiedEmail, updated.Id); err != nil {
		return nil, err
	}
	// Identity ownership is established before any profile or role mutation. A
	// conflicting subject therefore rolls the entire callback back without
	// partially changing an unrelated account.
	if err := ensureOIDCIdentityTx(db, &updated, issuer, subject, identitySubject, verifiedEmail); err != nil {
		return nil, err
	}

	role := oidcMappedRole(groups, &updated)
	roleChanged := role != updated.Role
	updates := map[string]any{
		"role": role,
	}
	if roleChanged {
		// OIDC group mappings are authorization state. Advance the version in the
		// same transaction as the role change so every previously issued cookie
		// becomes stale before the callback creates its replacement session.
		updates["session_version"] = gorm.Expr("session_version + ?", 1)
	}
	if verifiedEmail != "" {
		updates["email"] = verifiedEmail
		updates["verified_email"] = verifiedEmail
	}
	if displayName = truncateDisplayName(strings.TrimSpace(displayName)); displayName != "" {
		updates["display_name"] = displayName
	}
	result := db.Model(&model.User{}).Where("id = ?", updated.Id).Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, errors.New("oidc user update did not affect exactly one row")
	}
	var refreshed model.User
	if err := db.Omit("password").First(&refreshed, updated.Id).Error; err != nil {
		return nil, err
	}
	return &refreshed, nil
}

func ensureOIDCEmailAvailableTx(tx *gorm.DB, verifiedEmail string, userID int) error {
	if verifiedEmail == "" {
		return nil
	}
	var owners []model.User
	if err := tx.Unscoped().Where("LOWER(email) = LOWER(?) AND id <> ?", verifiedEmail, userID).Limit(1).Find(&owners).Error; err != nil {
		return err
	}
	if len(owners) > 0 {
		return errOAuthEmailConflict
	}
	return nil
}

func ensureOIDCIdentityTx(tx *gorm.DB, user *model.User, issuer string, subject string, identitySubject string, verifiedEmail string) error {
	owner, _, err := loadOIDCIdentityOwnerTx(tx, identitySubject)
	if err != nil {
		return err
	}
	if owner != nil {
		if owner.DeletedAt.Valid || owner.Id != user.Id {
			return errOAuthIdentityConflict
		}
		if verifiedEmail != "" {
			return tx.Model(&model.UserIdentity{}).
				Where("provider = ? AND subject = ? AND user_id = ?", oidcIdentityProvider, identitySubject, user.Id).
				Update("email", verifiedEmail).Error
		}
		return nil
	}

	var current model.UserIdentity
	err = tx.Where("user_id = ? AND provider = ?", user.Id, oidcIdentityProvider).First(&current).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return tx.Create(&model.UserIdentity{
			UserID:   user.Id,
			Provider: oidcIdentityProvider,
			Subject:  identitySubject,
			Email:    verifiedEmail,
		}).Error
	case err != nil:
		return err
	default:
		// This user already carries a different OIDC identity. Replacing it
		// because a new callback presents the same email would let one issuer
		// take over another's account.
		return errOAuthIdentityConflict
	}
}

func findExistingOIDCUser(issuer string, subject string, verifiedEmail string) (*model.User, error) {
	if model.CoreDB() == nil {
		return nil, errors.New("database is not initialized")
	}
	return findExistingOIDCUserTx(model.CoreDB(), canonicalOIDCIssuer(issuer), strings.TrimSpace(subject), strings.TrimSpace(verifiedEmail))
}

// findExistingOIDCUserTx treats the external identity as the authority; a
// verified email is the only fallback, for linking an existing account.
func findExistingOIDCUserTx(tx *gorm.DB, issuer string, subject string, verifiedEmail string) (*model.User, error) {
	identitySubject := oidcNamespacedSubject(issuer, subject)
	owner, _, err := loadOIDCIdentityOwnerTx(tx, identitySubject)
	if err != nil {
		return nil, err
	}
	if owner != nil {
		if owner.DeletedAt.Valid {
			return nil, errOAuthIdentityConflict
		}
		return owner, nil
	}

	if verifiedEmail == "" {
		return nil, nil
	}
	var users []model.User
	if err := tx.Unscoped().Where("LOWER(email) = LOWER(?)", verifiedEmail).Limit(2).Find(&users).Error; err != nil {
		return nil, err
	}
	if len(users) > 1 {
		return nil, errOAuthEmailConflict
	}
	if len(users) == 1 {
		if users[0].DeletedAt.Valid {
			return nil, errOAuthEmailConflict
		}
		return &users[0], nil
	}
	return nil, nil
}

func loadOIDCIdentityOwnerTx(tx *gorm.DB, identitySubject string) (*model.User, *model.UserIdentity, error) {
	if identitySubject == "" {
		return nil, nil, nil
	}
	var identity model.UserIdentity
	if err := tx.Where("provider = ? AND subject = ?", oidcIdentityProvider, identitySubject).First(&identity).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var owner model.User
	if err := tx.Unscoped().First(&owner, identity.UserID).Error; err != nil {
		// An identity without an owner is corrupt and must never be rebound.
		return nil, &identity, errOAuthIdentityConflict
	}
	return &owner, &identity, nil
}

func canonicalOIDCIssuer(issuer string) string {
	return strings.TrimRight(strings.TrimSpace(issuer), "/")
}

func oidcNamespacedSubject(issuer string, subject string) string {
	sum := sha256.Sum256([]byte(canonicalOIDCIssuer(issuer) + "\x00" + strings.TrimSpace(subject)))
	return "v1:" + hex.EncodeToString(sum[:])
}

func createOIDCUserTx(db *gorm.DB, issuer string, subject string, identitySubject string, username string, email string, displayName string, role int) (*model.User, error) {
	password, err := randomOIDCString()
	if err != nil {
		return nil, err
	}
	username, err = uniqueOIDCUsernameTx(db, username)
	if err != nil {
		return nil, err
	}
	user := &model.User{
		Username:      username,
		DisplayName:   truncateDisplayName(displayName),
		Password:      password,
		Email:         email,
		VerifiedEmail: email,
		Role:          role,
		Status:        common.UserStatusEnabled,
		TermsVersion:  common.TermsVersion,
		TermsAgreedAt: time.Now().Unix(),
	}
	if user.DisplayName == "" {
		user.DisplayName = username
	}
	setting := user.GetSetting()
	setting.NeedsPassword = true
	user.SetSetting(setting)
	// OIDC `sub` is unique only within one issuer. Persist the namespaced digest
	// so an operator changing identity providers cannot collide with old rows.
	identity := &model.UserIdentity{
		Provider: oidcIdentityProvider,
		Subject:  identitySubject,
		Email:    email,
	}
	if err := user.InsertWithIdentityTx(db, identity); err != nil {
		return nil, err
	}
	return user, nil
}

func oidcMappedRole(groups []string, existing *model.User) int {
	if matchesOIDCRole(groups, common.OIDCAdminRoles) {
		return common.RoleRootUser
	}
	if existing != nil && !oidcRoleMappingConfigured() {
		return existing.Role
	}
	return common.RoleCommonUser
}

func oidcRoleMappingConfigured() bool {
	return len(csvSet(common.OIDCAdminRoles)) > 0
}

func matchesOIDCRole(groups []string, configured string) bool {
	allowed := csvSet(configured)
	if len(allowed) == 0 {
		return false
	}
	for _, group := range groups {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(group))]; ok {
			return true
		}
	}
	return false
}

func csvSet(raw string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\t' }) {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			result[item] = struct{}{}
		}
	}
	return result
}

func claimString(claims map[string]any, key string) string {
	if key == "" {
		return ""
	}
	value, ok := claims[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		if len(typed) > 0 {
			return fmt.Sprintf("%v", typed[0])
		}
	}
	return ""
}

func claimBool(claims map[string]any, key string) bool {
	value, ok := claims[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func claimStringSlice(claims map[string]any, key string) []string {
	value, ok := claims[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprintf("%v", item))
			if text != "" {
				result = append(result, text)
			}
		}
		return result
	case string:
		return strings.FieldsFunc(typed, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	default:
		return nil
	}
}

func oidcUsername(claims map[string]any, email string) string {
	for _, key := range []string{common.OIDCUsernameClaim, "preferred_username", "nickname", "name"} {
		if username := sanitizeOIDCUsername(claimString(claims, key)); username != "" {
			return username
		}
	}
	if at := strings.Index(email, "@"); at > 0 {
		return sanitizeOIDCUsername(email[:at])
	}
	return "oidc-user"
}

var oidcUsernameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizeOIDCUsername(username string) string {
	username = oidcUsernameSanitizer.ReplaceAllString(strings.TrimSpace(username), "-")
	username = strings.Trim(username, "-_")
	if len(username) > 20 {
		username = username[:20]
	}
	return username
}

func uniqueOIDCUsernameTx(db *gorm.DB, base string) (string, error) {
	if db == nil {
		return "", errors.New("database is not initialized")
	}
	base = sanitizeOIDCUsername(base)
	if base == "" {
		base = "oidc-user"
	}
	if len(base) > 20 {
		base = base[:20]
	}
	for i := 0; i < 100; i++ {
		candidate := base
		if i > 0 {
			suffix := fmt.Sprintf("-%d", i)
			prefix := base
			if len(prefix)+len(suffix) > 20 {
				prefix = prefix[:20-len(suffix)]
			}
			candidate = prefix + suffix
		}
		var count int64
		if err := db.Unscoped().Model(&model.User{}).Where("username = ?", candidate).Limit(1).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
	return "", errors.New("unable to allocate oidc username")
}

func randomOIDCString() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func oidcSecureCookie() *securecookie.SecureCookie {
	return securecookie.New([]byte(common.SessionSecret), nil)
}

func setOIDCTransaction(c *gin.Context, tx oidcTransaction) error {
	encoded, err := oidcSecureCookie().Encode(oidcTransactionCookie, tx)
	if err != nil {
		return err
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcTransactionCookie, encoded, oidcTransactionMaxAge, "/api/auth/oidc", "", requestIsSecure(c), true)
	return nil
}

func readOIDCTransaction(c *gin.Context) (oidcTransaction, error) {
	raw, err := c.Cookie(oidcTransactionCookie)
	if err != nil {
		return oidcTransaction{}, err
	}
	var tx oidcTransaction
	if err := oidcSecureCookie().Decode(oidcTransactionCookie, raw, &tx); err != nil {
		return oidcTransaction{}, err
	}
	return tx, nil
}

func clearOIDCTransaction(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcTransactionCookie, "", -1, "/api/auth/oidc", "", requestIsSecure(c), true)
}

func sessionUserID(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func sanitizedOIDCNext(next string) string {
	if next == "" {
		next = oidcDefaultPostLoginPath()
	}
	// Browsers treat backslashes as path separators when resolving HTTP URLs and
	// strip some control characters. Reject both before parsing so a value such
	// as `/\\evil.example` cannot become a protocol-relative redirect client-side.
	if strings.ContainsRune(next, '\\') || strings.IndexFunc(next, unicode.IsControl) >= 0 {
		return "/console"
	}
	parsed, err := url.Parse(next)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") {
		return "/console"
	}
	return parsed.String()
}

func oidcDefaultPostLoginPath() string {
	raw := strings.TrimSpace(common.OIDCPostLoginRedirectURL)
	if raw == "" {
		return "/console"
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "/console"
	}
	if !parsed.IsAbs() {
		return raw
	}
	if parsed.Path == "" {
		return "/console"
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = parsed.Path
	}
	if path == "" {
		path = "/"
	}
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	return path
}

func oidcFrontendCallbackURL(next string) string {
	callbackPath := "/auth/oidc/callback?next=" + url.QueryEscape(sanitizedOIDCNext(next))
	if origin := oidcFrontendOrigin(); origin != "" {
		return origin + callbackPath
	}
	return callbackPath
}

func oidcFrontendOrigin() string {
	for _, raw := range []string{common.OIDCFrontendBaseURL, common.OIDCPostLoginRedirectURL, os.Getenv("FRONTEND_BASE_URL")} {
		origin := absoluteHTTPOrigin(raw)
		if origin != "" {
			return origin
		}
	}
	return ""
}

func absoluteHTTPOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func requestBaseURL(c *gin.Context) (string, error) {
	if c == nil || c.Request == nil {
		return "", errors.New("oidc request context is required")
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	host := c.Request.Host
	trustedProxy := common.RequestFromTrustedProxy(c)
	if trustedProxy {
		if forwardedProto := firstForwardedHeaderValue(c.GetHeader("X-Forwarded-Proto")); forwardedProto != "" {
			forwardedProto = strings.ToLower(forwardedProto)
			if forwardedProto != "http" && forwardedProto != "https" {
				return "", errors.New("invalid oidc forwarded protocol")
			}
			scheme = forwardedProto
		}
		if forwardedHost := firstForwardedHeaderValue(c.GetHeader("X-Forwarded-Host")); forwardedHost != "" {
			host = forwardedHost
		}
	}
	host, err := normalizeOIDCRedirectHost(host)
	if err != nil {
		return "", err
	}
	if !trustedProxy && !isLocalOIDCRedirectHost(host) {
		return "", errors.New("OIDC_REDIRECT_URL is required when deriving redirect URI from an untrusted public host")
	}
	return scheme + "://" + host, nil
}

func requestIsSecure(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	if common.RequestFromTrustedProxy(c) && strings.EqualFold(firstForwardedHeaderValue(c.GetHeader("X-Forwarded-Proto")), "https") {
		return true
	}
	return c.Request.TLS != nil
}

func firstForwardedHeaderValue(raw string) string {
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizeOIDCRedirectHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("oidc redirect host is required")
	}
	if strings.ContainsAny(raw, "/?#@\r\n\t ") {
		return "", errors.New("invalid oidc redirect host")
	}
	parsed, err := url.Parse("http://" + raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return "", errors.New("invalid oidc redirect host")
	}
	port := parsed.Port()
	if strings.Contains(parsed.Host, ":") && !strings.HasPrefix(parsed.Host, "[") && port == "" {
		return "", errors.New("invalid oidc redirect host")
	}
	if strings.Contains(parsed.Host, "]:") && port == "" {
		return "", errors.New("invalid oidc redirect host")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value <= 0 || value > 65535 {
			return "", errors.New("invalid oidc redirect host")
		}
	}
	return parsed.Host, nil
}

func isLocalOIDCRedirectHost(host string) bool {
	parsed, err := url.Parse("http://" + host)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	hostname := strings.Trim(parsed.Hostname(), "[]")
	lower := strings.ToLower(hostname)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}
