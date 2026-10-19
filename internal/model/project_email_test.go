package model

import (
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestProjectNoticesHaveTheirOwnSubjectInBothLanguages(t *testing.T) {
	db := setupProjectTestDB(t, true)
	admin := User{Username: "notice-admin", DisplayName: "Grace", Password: "x", Email: "admin@example.com"}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	project := Project{Name: "北辰实验室", PublicID: "p_notice"}
	target := ProjectMember{UserID: admin.Id + 1000, Role: ProjectRoleMember, Status: ProjectMemberActive}
	budget := billing.USD(150)
	notices := []projectNotice{
		{Kind: noticeMembership, ActorID: admin.Id},
		{Kind: noticeRestored, ActorID: admin.Id},
		{Kind: noticeBudget, ActorID: admin.Id, Budget: &budget},
		{Kind: noticeRemoved, ActorID: admin.Id},
		{Kind: noticeDissolved, ActorID: admin.Id},
		{Kind: noticeBudgetRequest, ActorID: admin.Id, Action: "approved", Budget: &budget},
		{Kind: noticeKeyRotated, ActorID: admin.Id, TokenName: "ci-runner"},
		{Kind: noticeKeyRevealed, ActorID: admin.Id, TokenName: "ci-runner"},
	}
	for _, lang := range []string{common.MailZH, common.MailEN} {
		seen := map[string]string{}
		for _, notice := range notices {
			subject, body, err := renderProjectNotice(db, lang, project, target, notice, time.Now())
			if err != nil {
				t.Fatalf("%s/%s: %v", lang, notice.Kind, err)
			}
			if subject == "" || subject == "Lazu project update" {
				t.Fatalf("%s/%s has a generic subject", lang, notice.Kind)
			}
			if other, dup := seen[subject]; dup {
				t.Fatalf("%s: %s and %s share subject %q", lang, other, notice.Kind, subject)
			}
			seen[subject] = notice.Kind
			if !strings.Contains(body, "lazu-email-logo.png") || !strings.Contains(body, "北辰实验室") {
				t.Fatalf("%s/%s is not a paper-tier project mail", lang, notice.Kind)
			}
			if notice.Kind == noticeBudget && !strings.Contains(body, "$150.00") {
				t.Fatalf("%s budget notice lost the new budget", lang)
			}
			if notice.Kind != noticeRemoved && notice.Kind != noticeRestored && !strings.Contains(body, "Grace") {
				t.Fatalf("%s/%s does not say who acted", lang, notice.Kind)
			}
		}
	}
}

func TestProjectInviteReadsInInvitersLanguage(t *testing.T) {
	db := setupProjectTestDB(t, true)
	inviter := User{Username: "inviter", Password: "x", Email: "owner@example.com"}
	inviter.SetSetting(dto.UserSetting{Language: "en"})
	if err := db.Create(&inviter).Error; err != nil {
		t.Fatal(err)
	}
	project := Project{ID: 42, Name: "Northstar", PublicID: "p_invite"}
	invite := ProjectInvite{Email: "lin@example.com", Role: ProjectRoleMember, InvitedBy: inviter.Id, TokenDigest: "digest-1", ExpiresAt: time.Now().Add(72 * time.Hour).Unix()}
	if err := queueProjectInviteTx(db, project, invite, "raw-token", "https://lazu.ai"); err != nil {
		t.Fatal(err)
	}
	var row ProjectNotification
	if err := db.Where("event_key = ?", "invite:digest-1").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	email, err := row.Email()
	if err != nil {
		t.Fatal(err)
	}
	if email.Subject != "inviter invited you to Northstar" || !strings.Contains(email.HTML, "https://lazu.ai/invite/raw-token") {
		t.Fatalf("unexpected invitation: %q", email.Subject)
	}
}
