package router

import (
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestWriteDecisionLogKeepsRequestIdentity(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:router_decision_log?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	defer func() { model.SetCoreDB(previousDB) }()
	if err := db.AutoMigrate(&model.RoutingDecision{}); err != nil {
		t.Fatalf("failed to migrate routing decision: %v", err)
	}

	id := WriteDecisionLog(
		&RequestContext{RequestID: "runtime-identity-request", UserID: 5, SessionID: "sess-1", ResolvedModel: "gpt-test"},
		&Decision{ChannelID: 1, Model: "gpt-test", Provider: "test", Meta: map[string]any{}},
	)
	if id == 0 {
		t.Fatal("expected routing decision id")
	}

	var stored model.RoutingDecision
	if err := model.CoreDB().First(&stored, id).Error; err != nil {
		t.Fatalf("failed to load routing decision: %v", err)
	}
	if !stored.IsPrimary || stored.StrategyName != "baseline" {
		t.Fatalf("unexpected baseline decision: %+v", stored)
	}
	if stored.UserID == nil || *stored.UserID != 5 || stored.SessionID != "sess-1" || stored.ChoseModel != "gpt-test" {
		t.Fatalf("decision lost its request identity: %+v", stored)
	}
}
