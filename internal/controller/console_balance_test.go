package controller

import (
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestProjectTokenDisplayRemainingKeepsEditorQuotaAndSourceUntouched(t *testing.T) {
	project := model.Project{ID: 1, PublicID: "prj_display", OwnerUserID: 1, Settings: model.JSONValue(`{"who_can_topup":"owner_admin","warning_pct":80}`)}
	member := model.ProjectMember{ProjectID: 1, UserID: 1, Role: model.ProjectRoleOwner, Status: model.ProjectMemberActive}
	pc := middleware.ProjectContext{Project: project, Member: member}
	token := &model.Token{Id: 2, ProjectID: 1, UserId: 1, PublicID: "tok_display", RemainQuota: 1000, UsedQuota: 200}
	facts := projectTokenFacts{Now: time.Now(), Members: map[int]model.ProjectMember{1: member}, Owners: map[int]model.User{1: {Id: 1}}, Payer: model.User{Quota: 5000}}
	editor, err := projectTokenPayloadWithFacts(nil, pc, token, facts)
	if err != nil {
		t.Fatal(err)
	}
	facts.DisplayRemaining = map[int]billing.MicroUSD{2: 800}
	display, err := projectTokenPayloadWithFacts(nil, pc, token, facts)
	if err != nil {
		t.Fatal(err)
	}
	if editor["remain_quota"] != billing.MicroUSD(1000) || display["remain_quota"] != billing.MicroUSD(800) {
		t.Fatalf("editor/display=%v/%v", editor["remain_quota"], display["remain_quota"])
	}
	if quota := display["effective_limit"].(gin.H); quota["kind"] != "key" || quota["remaining_microusd"] != billing.MicroUSD(800) {
		t.Fatalf("effective display=%v", quota)
	}
	if token.RemainQuota != 1000 || token.UsedQuota != 200 || display["used_quota"] != billing.MicroUSD(200) {
		t.Fatal("display overwrote Core Key/grant/used sources")
	}
	token.UnlimitedQuota = true
	display, err = projectTokenPayloadWithFacts(nil, pc, token, facts)
	if err != nil || display["remain_quota"] != billing.MicroUSD(1000) || display["effective_limit"].(gin.H)["kind"] != "balance" {
		t.Fatalf("unlimited display changed=%v %v", display, err)
	}
}

func TestDecorateUserListCopiesCommittedUserQuotaForDisplay(t *testing.T) {
	users := []*model.User{{Id: 1, Quota: 1000}, {Id: 2, Quota: -9007199254740993}}
	items, err := decorateUserList(users, false)
	if err != nil || len(items) != 2 {
		t.Fatalf("read-only list=%v %v", items, err)
	}
	items[0].Quota = 9007199254740993
	if users[0].Quota != 1000 || users[1].Quota != -9007199254740993 {
		t.Fatal("display list shares editable Core quota source")
	}
}
