package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

func bindingUser(t *testing.T, name, email string) *model.User {
	t.Helper()
	user := &model.User{Username: name, Password: "password123", Email: email, VerifiedEmail: email, Status: common.UserStatusEnabled}
	if err := user.Insert(0); err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().First(user, user.Id).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func issueBindingProofs(t *testing.T, user *model.User, email string) *emailBindingRequest {
	t.Helper()
	key := emailBindingKey(user, email)
	common.RegisterVerificationCodeWithKey(key, "123456", emailBindingPurpose)
	common.RegisterVerificationCodeWithKey(key, "654321", emailBindingCurrentPurpose)
	common.ClearVerificationFailures(key, emailBindingPurpose)
	common.ClearVerificationFailures(key, emailBindingCurrentPurpose)
	t.Cleanup(func() { common.DeleteKey(key, emailBindingPurpose); common.DeleteKey(key, emailBindingCurrentPurpose) })
	return &emailBindingRequest{Email: email, Code: "123456", CurrentCode: "654321"}
}

func TestEmailBindingProofsAndOwnership(t *testing.T) {
	cases := []string{"initial", "replace", "same address", "missing old proof", "other account", "other target", "login code", "revoked session", "changed password", "changed email", "occupied after issue", "deleted owner", "case variant owner", "verified alias owner", "exhausted attempts"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			setupOIDCTestDB(t)
			oldRedis := common.RedisEnabled
			common.RedisEnabled = false
			t.Cleanup(func() { common.RedisEnabled = oldRedis })
			email := "old@example.com"
			if name == "initial" {
				email = ""
			}
			user := bindingUser(t, "binder", email)
			target := "new@example.com"
			if name == "same address" {
				target = email
			}
			request := issueBindingProofs(t, user, target)
			wantSuccess := name == "initial" || name == "replace" || name == "same address"
			switch name {
			case "missing old proof":
				request.CurrentCode = ""
			case "other account":
				user = bindingUser(t, "other", "")
			case "other target":
				request.Email = "different@example.com"
			case "login code":
				common.DeleteKey(emailBindingKey(user, target), emailBindingPurpose)
				common.RegisterVerificationCodeWithKey(target, request.Code, common.EmailAuthPurpose)
				t.Cleanup(func() { common.DeleteKey(target, common.EmailAuthPurpose) })
			case "revoked session":
				model.CoreDB().Model(&model.User{}).Where("id = ?", user.Id).Update("session_version", user.SessionVersion+1)
			case "changed password":
				model.CoreDB().Model(&model.User{}).Where("id = ?", user.Id).Update("password", "different-hash")
			case "changed email":
				model.CoreDB().Model(&model.User{}).Where("id = ?", user.Id).Update("email", "changed@example.com")
			case "occupied after issue", "deleted owner", "case variant owner", "verified alias owner":
				ownerEmail := target
				if name == "case variant owner" {
					ownerEmail = "NEW@Example.com"
				}
				if name == "verified alias owner" {
					ownerEmail = "another@example.com"
				}
				owner := bindingUser(t, "owner", ownerEmail)
				if name == "deleted owner" {
					model.CoreDB().Delete(owner)
				}
				if name == "verified alias owner" {
					model.CoreDB().Model(owner).Update("verified_email", target)
				}
			case "exhausted attempts":
				bad := *request
				bad.Code = "000000"
				for i := 0; i < common.MaxVerificationAttempts; i++ {
					if bindEmail(user, &bad) == nil {
						t.Fatal("wrong proof accepted")
					}
				}
			}
			err := bindEmail(user, request)
			if (err == nil) != wantSuccess {
				t.Fatalf("success=%v, err=%v", wantSuccess, err)
			}
			if wantSuccess {
				var stored model.User
				model.CoreDB().First(&stored, user.Id)
				if stored.Email != target || stored.VerifiedEmail != target {
					t.Fatalf("mailbox not persisted: %+v", stored.Id)
				}
				if bindEmail(&stored, request) == nil {
					t.Fatal("proof replay accepted")
				}
			}
		})
	}
}

func TestEmailBindingConcurrentOwners(t *testing.T) {
	setupOIDCTestDB(t)
	users := []*model.User{bindingUser(t, "first", ""), bindingUser(t, "second", "")}
	proofs := []*emailBindingRequest{issueBindingProofs(t, users[0], "shared@example.com"), issueBindingProofs(t, users[1], "shared@example.com")}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range users {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results <- bindEmail(users[i], proofs[i]) }(i)
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected one owner, got %d", successes)
	}
}

func TestEmailBindingRequiresCurrentSession(t *testing.T) {
	for _, mode := range []string{"session", "management token", "missing session", "stale session"} {
		t.Run(mode, func(t *testing.T) {
			setupOIDCTestDB(t)
			user := bindingUser(t, "session-binder", "")
			proof := issueBindingProofs(t, user, "new@example.com")
			engine := gin.New()
			engine.Use(sessions.Sessions("binding-test", cookie.NewStore([]byte("binding-test-cookie-secret"))))
			engine.POST("/bind", func(c *gin.Context) {
				c.Set("id", user.Id)
				if mode != "missing session" {
					s := sessions.Default(c)
					s.Set("id", user.Id)
					version := user.SessionVersion
					if mode == "stale session" {
						version++
					}
					s.Set("session_version", version)
				}
				c.Set("use_access_token", mode == "management token")
				EmailBind(c)
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(fmt.Sprintf(`{"email":%q,"code":%q}`, proof.Email, proof.Code)))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(w, req)
			if (w.Code == http.StatusOK) != (mode == "session") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
