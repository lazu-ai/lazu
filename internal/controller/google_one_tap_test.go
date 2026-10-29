package controller

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/go-jose/go-jose/v4"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

const googleOneTapTestClientID = "one-tap-client.apps.googleusercontent.com"

// googleOneTapTestSigner stands in for Google: it signs ID tokens with a
// local key and the verifier under test trusts only that key.
type googleOneTapTestSigner struct {
	t      *testing.T
	signer jose.Signer
}

func setupGoogleOneTapTest(t *testing.T) *googleOneTapTestSigner {
	t.Helper()
	gin.SetMode(gin.TestMode)
	setupOIDCTestDB(t)
	t.Cleanup(waitForAuthEmailDeliveries)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	previous := struct {
		clientID, clientSecret, sessionSecret string
		enabled, register                     bool
		verifier                              func(context.Context) (*oidc.IDTokenVerifier, error)
	}{
		common.GoogleOAuthClientID, common.GoogleOAuthClientSecret, common.SessionSecret,
		common.GoogleOneTapEnabled, common.RegisterEnabled, googleOneTapVerifier,
	}
	t.Cleanup(func() {
		common.GoogleOAuthClientID = previous.clientID
		common.GoogleOAuthClientSecret = previous.clientSecret
		common.SessionSecret = previous.sessionSecret
		common.GoogleOneTapEnabled = previous.enabled
		common.RegisterEnabled = previous.register
		googleOneTapVerifier = previous.verifier
	})
	common.GoogleOAuthClientID = googleOneTapTestClientID
	common.GoogleOAuthClientSecret = "placeholder-google-secret"
	common.SessionSecret = "placeholder-session-value-with-enough-entropy"
	common.GoogleOneTapEnabled = true
	common.RegisterEnabled = true
	keys := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{key.Public()}}
	googleOneTapVerifier = func(context.Context) (*oidc.IDTokenVerifier, error) {
		return oidc.NewVerifier(googleOIDCIssuer, keys, &oidc.Config{ClientID: common.GoogleOAuthClientID}), nil
	}
	return &googleOneTapTestSigner{t: t, signer: signer}
}

// token signs claims for our client; overrides replace or remove (nil) any of
// the defaults.
func (s *googleOneTapTestSigner) token(nonce string, overrides map[string]any) string {
	s.t.Helper()
	claims := map[string]any{
		"iss":            googleOIDCIssuer,
		"aud":            googleOneTapTestClientID,
		"sub":            "google-subject-1",
		"email":          "one.tap@example.com",
		"email_verified": true,
		"name":           "One Tap",
		"nonce":          nonce,
		"iat":            time.Now().Unix(),
		"exp":            time.Now().Add(time.Hour).Unix(),
	}
	for key, value := range overrides {
		if value == nil {
			delete(claims, key)
		} else {
			claims[key] = value
		}
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		s.t.Fatalf("marshal claims: %v", err)
	}
	signed, err := s.signer.Sign(payload)
	if err != nil {
		s.t.Fatalf("sign token: %v", err)
	}
	serialized, err := signed.CompactSerialize()
	if err != nil {
		s.t.Fatalf("serialize token: %v", err)
	}
	return serialized
}

func googleOneTapTestEngine() *gin.Engine {
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("google-one-tap-test-session-secret"))))
	engine.GET("/api/auth/oidc/status", OIDCStatus)
	engine.GET("/api/auth/google/one-tap", GoogleOneTapNonce)
	engine.POST("/api/auth/google/one-tap", GoogleOneTapLogin)
	return engine
}

// startGoogleOneTap asks for a nonce and returns it with the cookie that
// carries it.
func startGoogleOneTap(t *testing.T, engine *gin.Engine) (string, *http.Cookie) {
	t.Helper()
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/auth/google/one-tap", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("nonce: expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data struct {
			Nonce string `json:"nonce"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Data.Nonce == "" {
		t.Fatalf("nonce: unexpected body %s (%v)", recorder.Body.String(), err)
	}
	for _, issued := range recorder.Result().Cookies() {
		if issued.Name == googleOneTapCookie {
			if issued.Path != googleOneTapCookiePath || !issued.HttpOnly {
				t.Fatalf("nonce cookie must be HttpOnly and scoped: %+v", issued)
			}
			return body.Data.Nonce, issued
		}
	}
	t.Fatalf("nonce: no %s cookie in %v", googleOneTapCookie, recorder.Result().Cookies())
	return "", nil
}

func postGoogleOneTap(engine *gin.Engine, credential string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	body, _ := json.Marshal(googleOneTapLoginRequest{Credential: credential})
	request := httptest.NewRequest(http.MethodPost, "/api/auth/google/one-tap", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	for _, item := range cookies {
		request.AddCookie(item)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func errorCodeOf(t *testing.T, recorder *httptest.ResponseRecorder) types.ErrorCode {
	t.Helper()
	var body struct {
		Error struct {
			Code types.ErrorCode `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", recorder.Body.String(), err)
	}
	return body.Error.Code
}

func TestGoogleOneTapIsOfferedOnlyWhenSwitchedOn(t *testing.T) {
	setupGoogleOneTapTest(t)
	engine := googleOneTapTestEngine()

	status := func() map[string]any {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/status", nil))
		var body struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode status: %v", err)
		}
		return body.Data
	}

	if got := status()["google_one_tap_client_id"]; got != googleOneTapTestClientID {
		t.Fatalf("status should carry the client id when One Tap is on, got %v", got)
	}

	common.GoogleOneTapEnabled = false
	if _, present := status()["google_one_tap_client_id"]; present {
		t.Fatal("status must not offer One Tap while it is switched off")
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/auth/google/one-tap", nil))
	if recorder.Code != http.StatusNotFound || errorCodeOf(t, recorder) != types.ErrorCodeOAuthProviderNotConfigured {
		t.Fatalf("nonce while off: got %d %s", recorder.Code, recorder.Body.String())
	}

	// Switched on but without a usable Google client is still off.
	common.GoogleOneTapEnabled = true
	common.GoogleOAuthClientSecret = ""
	if _, present := status()["google_one_tap_client_id"]; present {
		t.Fatal("status must not offer One Tap without a complete Google client")
	}
}

func TestGoogleOneTapSignsInWithTheNonceItIssued(t *testing.T) {
	signer := setupGoogleOneTapTest(t)
	engine := googleOneTapTestEngine()

	nonce, nonceCookie := startGoogleOneTap(t, engine)
	recorder := postGoogleOneTap(engine, signer.token(nonce, nil), nonceCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected sign-in, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || !body.Success || body.Data.ID == "" {
		t.Fatalf("unexpected sign-in body %s (%v)", recorder.Body.String(), err)
	}

	var sessionSet, nonceCleared bool
	for _, issued := range recorder.Result().Cookies() {
		switch issued.Name {
		case "session":
			sessionSet = issued.Value != ""
		case googleOneTapCookie:
			nonceCleared = issued.MaxAge < 0
		}
	}
	if !sessionSet || !nonceCleared {
		t.Fatalf("expected a session and a cleared nonce, got %v", recorder.Result().Cookies())
	}

	var identity model.UserIdentity
	if err := model.CoreDB().Where("provider = ? AND subject = ?", googleOAuthProvider, "google-subject-1").First(&identity).Error; err != nil {
		t.Fatalf("google identity not recorded: %v", err)
	}
	var user model.User
	if err := model.CoreDB().First(&user, identity.UserID).Error; err != nil {
		t.Fatalf("load signed-in account: %v", err)
	}
	if user.PublicID != body.Data.ID || user.VerifiedEmail != "one.tap@example.com" {
		t.Fatalf("the answer and the recorded identity disagree: answer %q, account %+v", body.Data.ID, user)
	}
}

func TestGoogleOneTapRefusesTokensItDidNotAskFor(t *testing.T) {
	signer := setupGoogleOneTapTest(t)
	engine := googleOneTapTestEngine()

	cases := []struct {
		name string
		run  func() *httptest.ResponseRecorder
		want types.ErrorCode
	}{
		{
			// A cross-site post cannot send the visitor's nonce cookie.
			name: "no nonce cookie",
			run: func() *httptest.ResponseRecorder {
				return postGoogleOneTap(engine, signer.token("anything", nil))
			},
			want: types.ErrorCodeOAuthStateInvalid,
		},
		{
			// The attacker's own token carries the attacker's nonce.
			name: "token for another nonce",
			run: func() *httptest.ResponseRecorder {
				_, nonceCookie := startGoogleOneTap(t, engine)
				return postGoogleOneTap(engine, signer.token("someone-elses-nonce", nil), nonceCookie)
			},
			want: types.ErrorCodeOAuthIdentityInvalid,
		},
		{
			name: "token for another client",
			run: func() *httptest.ResponseRecorder {
				nonce, nonceCookie := startGoogleOneTap(t, engine)
				return postGoogleOneTap(engine, signer.token(nonce, map[string]any{"aud": "another-client"}), nonceCookie)
			},
			want: types.ErrorCodeOAuthIdentityInvalid,
		},
		{
			name: "expired token",
			run: func() *httptest.ResponseRecorder {
				nonce, nonceCookie := startGoogleOneTap(t, engine)
				return postGoogleOneTap(engine, signer.token(nonce, map[string]any{
					"iat": time.Now().Add(-2 * time.Hour).Unix(),
					"exp": time.Now().Add(-time.Hour).Unix(),
				}), nonceCookie)
			},
			want: types.ErrorCodeOAuthIdentityInvalid,
		},
		{
			name: "unverified address",
			run: func() *httptest.ResponseRecorder {
				nonce, nonceCookie := startGoogleOneTap(t, engine)
				return postGoogleOneTap(engine, signer.token(nonce, map[string]any{"email_verified": false}), nonceCookie)
			},
			want: types.ErrorCodeOAuthEmailUnverified,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := tc.run()
			if recorder.Code == http.StatusOK {
				t.Fatalf("expected refusal, got sign-in: %s", recorder.Body.String())
			}
			if got := errorCodeOf(t, recorder); got != tc.want {
				t.Fatalf("expected %s, got %s (%s)", tc.want, got, recorder.Body.String())
			}
		})
	}

	var count int64
	if err := model.CoreDB().Model(&model.User{}).Count(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 0 {
		t.Fatalf("a refused token created %d account(s)", count)
	}
}
