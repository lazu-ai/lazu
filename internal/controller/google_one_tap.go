package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/securecookie"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/types"
)

// Google One Tap signs a visitor in without leaving the page: Google's script
// hands the browser an ID token for our client, the browser posts it here, and
// the account is resolved exactly as the redirect callback resolves it.
//
// The nonce is issued by the server, not made up by the page. It lives in a
// signed, HttpOnly cookie scoped to these two routes, expires with it, and is
// cleared by the first post, so a token is accepted only by the browser that
// asked for it:
// a stolen token cannot be replayed from elsewhere, and a page on another site
// cannot sign a visitor into the attacker's account by posting the attacker's
// token (it can read neither the visitor's nonce nor, on a cross-site post,
// send the cookie).
const (
	googleOneTapCookie     = "lazu_google_one_tap"
	googleOneTapCookiePath = "/api/auth/google"
	googleOneTapNonceTTL   = 10 * 60
)

type googleOneTapLoginRequest struct {
	Credential string `json:"credential"`
}

// googleOneTapVerifier builds the verifier for Google ID tokens issued to our
// client. Tests replace it with one backed by a local key.
var googleOneTapVerifier = func(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	provider, err := googleOIDCProvider(ctx)
	if err != nil {
		return nil, err
	}
	return provider.Verifier(&oidc.Config{ClientID: common.GoogleOAuthClientID}), nil
}

// GoogleOneTapNonce starts a One Tap sign-in: it answers with the nonce the
// page passes to Google and keeps the same value in the cookie.
func GoogleOneTapNonce(c *gin.Context) {
	if !common.GoogleOneTapAvailable() {
		httperr.Code(c, types.ErrorCodeOAuthProviderNotConfigured)
		return
	}
	nonce, err := randomOIDCString()
	if err != nil {
		common.SysLog(fmt.Sprintf("google one tap nonce generation failed: %v", err))
		httperr.Code(c, types.ErrorCodeOAuthLoginFailed)
		return
	}
	encoded, err := googleOneTapSecureCookie().Encode(googleOneTapCookie, nonce)
	if err != nil {
		common.SysLog(fmt.Sprintf("google one tap nonce cookie failed: %v", err))
		httperr.Code(c, types.ErrorCodeOAuthLoginFailed)
		return
	}
	setGoogleOneTapCookie(c, encoded, googleOneTapNonceTTL)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"nonce": nonce},
	})
}

// GoogleOneTapLogin finishes a One Tap sign-in with the ID token Google gave
// the page. It answers like every other sign-in that stays on the page: the
// signed-in user, with the session cookie set.
func GoogleOneTapLogin(c *gin.Context) {
	if !common.GoogleOneTapAvailable() {
		httperr.Code(c, types.ErrorCodeOAuthProviderNotConfigured)
		return
	}
	var request googleOneTapLoginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}
	credential := strings.TrimSpace(request.Credential)
	if credential == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "credential"})
		return
	}
	// One nonce, one attempt: consume it before anything can fail.
	nonce := readGoogleOneTapNonce(c)
	setGoogleOneTapCookie(c, "", -1)
	if nonce == "" {
		httperr.Code(c, types.ErrorCodeOAuthStateInvalid)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	verifier, err := googleOneTapVerifier(ctx)
	if err != nil {
		common.SysLog(fmt.Sprintf("google one tap verifier unavailable: %v", err))
		httperr.Code(c, types.ErrorCodeOAuthProviderError)
		return
	}
	identity, err := googleIdentityFromIDToken(ctx, verifier, credential, nonce)
	if err != nil {
		common.SysLog(fmt.Sprintf("google one tap identity rejected: %v", err))
		if errors.Is(err, errOAuthNoVerifiedEmail) {
			httperr.Code(c, types.ErrorCodeOAuthEmailUnverified)
		} else {
			httperr.Code(c, types.ErrorCodeOAuthIdentityInvalid)
		}
		return
	}
	identity.MailLang = requestMailLang(c)
	identity.MailSurface = requestHomeSurface(c)
	user, err := resolveSocialOAuthUser(identity)
	if err != nil {
		common.SysLog(fmt.Sprintf("google one tap user resolution failed: %v", err))
		httperr.Code(c, oauthRedirectCode(err))
		return
	}
	if user.Status != common.UserStatusEnabled {
		httperr.Code(c, types.ErrorCodeUserBanned)
		return
	}
	setupLogin(user, c)
}

// The cookie follows the session cookie's SameSite policy, so a deployment
// whose console and API are on different sites (SameSite=None) works the same
// way its session does.
func setGoogleOneTapCookie(c *gin.Context, value string, maxAge int) {
	c.SetSameSite(common.ResolveSessionCookieSameSite())
	secure := common.ResolveSessionCookieSecure() || requestIsSecure(c)
	c.SetCookie(googleOneTapCookie, value, maxAge, googleOneTapCookiePath, "", secure, true)
}

// The signature carries its own timestamp; a nonce older than its cookie is
// refused even if a browser kept the cookie.
func googleOneTapSecureCookie() *securecookie.SecureCookie {
	return oidcSecureCookie().MaxAge(googleOneTapNonceTTL)
}

func readGoogleOneTapNonce(c *gin.Context) string {
	raw, err := c.Cookie(googleOneTapCookie)
	if err != nil || raw == "" {
		return ""
	}
	var nonce string
	if err := googleOneTapSecureCookie().Decode(googleOneTapCookie, raw, &nonce); err != nil {
		return ""
	}
	return nonce
}
