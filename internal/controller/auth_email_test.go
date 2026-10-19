package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

func TestVerificationTreatsEmailOwnershipAsCaseInsensitiveAndUnscoped(t *testing.T) {
	setupOIDCTestDB(t)
	owner := model.User{
		Username: "reserved-email-owner",
		Password: "placeholder-password",
		Email:    "owner@example.com",
	}
	if err := model.CoreDB().Create(&owner).Error; err != nil {
		t.Fatalf("seed email owner: %v", err)
	}
	if err := model.CoreDB().Delete(&owner).Error; err != nil {
		t.Fatalf("soft-delete email owner: %v", err)
	}

	previousDeliver := deliverAuthEmail
	var subject, receiver string
	deliverAuthEmail = func(gotSubject string, gotReceiver string, _ string) error {
		subject = gotSubject
		receiver = gotReceiver
		return nil
	}
	t.Cleanup(func() { deliverAuthEmail = previousDeliver })

	dispatchEmailVerification("Owner@Example.com", common.MailZH)
	if receiver != "Owner@Example.com" {
		t.Fatalf("account guidance sent to unexpected receiver %q", receiver)
	}
	wantSubject := fmt.Sprintf("这个邮箱已经有 %s 账号了", common.SystemName)
	if subject != wantSubject {
		t.Fatalf("case-variant reserved email received a registration code: subject=%q", subject)
	}
}

func TestWaitForAuthEmailDeliveriesDrainsAcceptedJobs(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	enqueueAuthEmailJob("auth-email-shutdown-test", func() {
		close(started)
		<-release
	})
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- WaitForAuthEmailDeliveries(ctx)
	}()

	select {
	case err := <-waitDone:
		t.Fatalf("drain returned before accepted job completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-waitDone; err != nil {
		t.Fatalf("drain accepted auth email job: %v", err)
	}
}

func TestWelcomeEmailKeepsConsoleAndAPIOriginsSeparate(t *testing.T) {
	previousFrontend := common.OIDCFrontendBaseURL
	previousServer := system_setting.ServerAddress
	common.OIDCFrontendBaseURL = "https://console.example.com"
	system_setting.ServerAddress = "https://api.example.com/"
	t.Cleanup(func() {
		common.OIDCFrontendBaseURL = previousFrontend
		system_setting.ServerAddress = previousServer
	})

	_, content := authWelcomeMail(common.MailEN, "Grace", "")
	if !strings.Contains(content, "https://api.example.com/v1") {
		t.Fatalf("welcome email does not use backend API origin: %s", content)
	}
	if !strings.Contains(content, "https://console.example.com/console") {
		t.Fatalf("welcome email does not use frontend console origin: %s", content)
	}
	if strings.Contains(content, "https://console.example.com/v1") {
		t.Fatal("welcome email incorrectly advertises the frontend as an API endpoint")
	}
}

// TestAuthEmailTemplatesRenderFully renders every identity mail in both
// languages and checks it stays on the paper tier of the locked mail system.
func TestAuthEmailTemplatesRenderFully(t *testing.T) {
	previousName := common.SystemName
	common.SystemName = "Lazu"
	t.Cleanup(func() { common.SystemName = previousName })

	for _, lang := range []string{common.MailZH, common.MailEN} {
		rendered := authMailPreviews(lang)
		if want := 8 + len(surfaceWelcomeMails); len(rendered) != want {
			t.Fatalf("expected %d identity mails, got %d", want, len(rendered))
		}
		for name, content := range rendered {
			if strings.Contains(content, "{{") {
				t.Fatalf("%s/%s still contains template syntax", lang, name)
			}
			if !strings.Contains(content, "lazu-email-logo.png") || !strings.Contains(content, "#fdfdfc") || !strings.Contains(content, "#202329") {
				t.Fatalf("%s/%s is not on the paper tier", lang, name)
			}
			if strings.Contains(content, "lazu-email-mark.png") || strings.Contains(content, "请勿回复") {
				t.Fatalf("%s/%s carries a retired mark or footer", lang, name)
			}
		}
		closed := rendered["registration_closed"]
		if strings.Contains(closed, "已经注册过") || strings.Contains(closed, "already have an account") {
			t.Fatal("registration-closed email must not claim that an account exists")
		}
		if lang == common.MailEN && strings.Contains(rendered["verification"], "验证") {
			t.Fatal("English verification mail contains Chinese copy")
		}

		// Opt-in dump so the templates can be eyeballed in a browser.
		outDir := strings.TrimSpace(os.Getenv("AUTH_EMAIL_PREVIEW_DIR"))
		if outDir == "" {
			continue
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatalf("create preview dir: %v", err)
		}
		for name, content := range rendered {
			path := filepath.Join(outDir, lang+"-"+name+".html")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s preview: %v", name, err)
			}
		}
	}
}

func TestUserMailLangPrefersStoredLanguage(t *testing.T) {
	user := &model.User{}
	user.SetSetting(dto.UserSetting{Language: "en"})
	if got := userMailLang(user, nil); got != common.MailEN {
		t.Fatalf("stored language ignored: %q", got)
	}
	if got := userMailLang(&model.User{}, nil); got != common.MailEN {
		t.Fatalf("default mail language = %q", got)
	}
}

func TestNormalizeLocaleTag(t *testing.T) {
	for raw, want := range map[string]string{"zh-CN,zh;q=0.9": "zh-CN", "en": "en", "zh-TW": "zh-TW", "<script>": "", "": "", "english-language-very-long": ""} {
		if got := normalizeLocaleTag(raw); got != want {
			t.Fatalf("normalizeLocaleTag(%q) = %q, want %q", raw, got, want)
		}
	}
}
