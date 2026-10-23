package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"
	"time"

	"gorm.io/gorm"
)

func projectLogPagePostgresFixture(t *testing.T) (*gorm.DB, ProjectLogAccess, int64, int64) {
	t.Helper()
	core, db := postgresTelemetryFixture(t)
	if err := core.AutoMigrate(&Project{}); err != nil {
		t.Fatal(err)
	}
	if err := core.Create(&Project{ID: 42, PublicID: "prj_log_page", OwnerUserID: 1, Name: "log page"}).Error; err != nil {
		t.Fatal(err)
	}
	oldGroup := logGroupCol
	logGroupCol = `"group"`
	t.Cleanup(func() { logGroupCol = oldGroup })
	// Preserve the legacy NULL project fallback as well as current zero IDs.
	if err := db.Exec("ALTER TABLE logs ALTER COLUMN project_id DROP NOT NULL").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return partitionTelemetryTableTx(tx, telemetryPartitionSpecs[0]) }); err != nil {
		t.Fatal(err)
	}
	if err := installEffortClampedColumn(db); err != nil {
		t.Fatal(err)
	}
	if err := ensureLogListIndexes(db, false); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	access := ProjectLogAccess{Project: Project{ID: 42, OwnerUserID: 1}, Viewer: ProjectMember{ProjectID: 42, UserID: 1, Role: ProjectRoleOwner, Status: ProjectMemberActive}}
	return db, access, start, start + 86400
}

func TestPostgresProjectLogPagesPreserveScopeFiltersAndCount(t *testing.T) {
	db, owner, start, end := projectLogPagePostgresFixture(t)
	row := func(id, project, actor int, at int64) Log {
		return Log{Id: id, PublicID: fmt.Sprintf("log_page_%d", id), RequestID: fmt.Sprintf("request-page-%d", id), RequestTS: at * 1000, CreatedAt: at,
			ProjectID: project, UserId: actor, TokenId: actor * 11, Type: LogTypeConsume, ModelName: "ordinary", TokenName: "ordinary-key", Group: "ordinary", Content: "ordinary", Other: `{}`}
	}
	rows := []Log{
		row(1, 42, 1, start+1), row(2, 42, 2, start+2), row(3, 42, 3, start+3), row(4, 43, 1, start+4),
		row(5, 0, 1, start+5), row(6, 0, 1, start+6), row(7, 0, 2, start+7), row(8, 42, 1, start+8),
		row(9, 42, 1, start-1), row(10, 42, 1, end+1), row(11, 42, 1, start), row(12, 42, 1, end),
		row(13, 42, 2, start+13), row(14, 42, 1, start+14), row(15, 42, 2, start+15), row(16, 42, 2, start+16), row(17, 42, 2, start+17),
	}
	rows[7].Type = LogTypeUnknown
	// created_at is authoritative: neither a different partition month nor an
	// undated physical request key may remove a visible log.
	rows[12].RequestTS = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	rows[13].RequestTS = 0
	rows[1].TokenName = "member-key"
	for _, i := range []int{14, 15} {
		rows[i].ModelName, rows[i].TokenName, rows[i].Group = "target", "member-key", "target-group"
	}
	rows[14].Type, rows[14].RejectionReason, rows[14].RequestID, rows[14].Content = LogTypeError, "quota", "reject-quota", "denied request"
	rows[15].Other = `{"effort_clamped":true}`
	rows[16].Type = LogTypeError
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&Log{}).Where("id = ?", 6).Update("project_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	// Rows 5 and 6 are the owner's project-less logs from before schema 37,
	// 7 another user's. The backfill must leave every page exactly as the
	// owner-fallback scope showed it.
	if err := backfillLogProjectIDs(coreDB, db, math.MinInt64); err != nil {
		t.Fatal(err)
	}
	var stillLegacy int64
	if err := db.Model(&Log{}).Where("(project_id = 0 OR project_id IS NULL) AND user_id = 1").Count(&stillLegacy).Error; err != nil || stillLegacy != 0 {
		t.Fatalf("owner rows left without a project: %d %v", stillLegacy, err)
	}
	base := ProjectLogFilter{Start: start, End: end}
	member := owner
	member.Viewer.UserID, member.Viewer.Role = 2, ProjectRoleMember
	suspended := member
	suspended.Viewer.Status, suspended.Viewer.Role = ProjectMemberSuspended, ProjectRoleAdmin
	actor := owner
	actorID := 2
	actor.ActorID = &actorID
	token := owner
	token.TokenID = 22
	invalid := owner
	invalid.Viewer.ProjectID = 43
	removed := member
	removed.Viewer.Status = ProjectMemberRemoved
	foreignActor := member
	otherID := 3
	foreignActor.ActorID = &otherID
	// Newest first by created_at: row 12 is at the window's end, 11 at its start.
	all := []int{12, 17, 16, 15, 14, 13, 6, 5, 3, 2, 1, 11}
	cases := []struct {
		name   string
		access ProjectLogAccess
		filter ProjectLogFilter
		ids    []int
	}{
		{"manager", owner, base, all},
		{"member", member, base, []int{17, 16, 15, 13, 2}},
		{"suspended-admin-personal", suspended, base, []int{17, 16, 15, 13, 2}},
		{"actor", actor, base, []int{17, 16, 15, 13, 2}},
		{"token", token, base, []int{17, 16, 15, 13, 2}},
		{"invalid-project", invalid, base, nil}, {"removed", removed, base, nil}, {"member-foreign-actor", foreignActor, base, nil},
		{"type", owner, ProjectLogFilter{Start: start, End: end, Type: LogTypeConsume}, []int{12, 16, 14, 13, 6, 5, 3, 2, 1, 11}},
		{"rejected-overrides-type", owner, ProjectLogFilter{Start: start, End: end, Type: LogTypeConsume, Rejected: true}, []int{17, 15}},
		{"effort", owner, ProjectLogFilter{Start: start, End: end, EffortClamped: true}, []int{16}},
		{"model", owner, ProjectLogFilter{Start: start, End: end, Model: "targ%"}, []int{16, 15}},
		{"token-name", owner, ProjectLogFilter{Start: start, End: end, TokenName: "member-key"}, []int{16, 15, 2}},
		{"group", owner, ProjectLogFilter{Start: start, End: end, Group: "target-group"}, []int{16, 15}},
		{"reason", owner, ProjectLogFilter{Start: start, End: end, Reason: "quota"}, []int{15}},
		{"unknown-reason", owner, ProjectLogFilter{Start: start, End: end, Reason: "unknown"}, []int{12, 17, 16, 14, 13, 6, 5, 3, 2, 1, 11}},
		{"request-keyword", owner, ProjectLogFilter{Start: start, End: end, Keyword: "reject-quota"}, []int{15}},
		{"content-keyword", owner, ProjectLogFilter{Start: start, End: end, Keyword: "denied"}, []int{15}},
		{"all-filters", member, ProjectLogFilter{Start: start, End: end, Type: LogTypeError, Model: "target", TokenName: "member-key", Group: "target-group", Reason: "quota", Keyword: "denied"}, []int{15}},
		{"inclusive-boundary", owner, ProjectLogFilter{Start: start, End: start}, []int{11}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var before []*Log
			if err := orderLogsNewestFirst(ProjectLogsQuery(test.access, test.filter)).Find(&before).Error; err != nil {
				t.Fatal(err)
			}
			for _, page := range [][2]int{{0, 100}, {1, 2}, {100, 5}, {0, 0}} {
				logs, total, err := ListProjectLogsContext(context.Background(), test.access, test.filter, page[0], page[1])
				if err != nil || total != int64(len(test.ids)) {
					t.Fatalf("total=%d want=%d err=%v", total, len(test.ids), err)
				}
				from, to := min(page[0], len(before)), min(page[0]+page[1], len(before))
				if len(logs) != to-from {
					t.Fatalf("page size=%d want=%d", len(logs), to-from)
				}
				for i, log := range logs {
					if log.Id != test.ids[from+i] || !reflect.DeepEqual(log, before[from+i]) {
						t.Fatal("page changed an original full row, scope, filter or order")
					}
				}
			}
		})
	}
	t.Run("duplicate-id-across-partitions", func(t *testing.T) {
		a, b := row(21, 42, 1, end+1000), row(21, 42, 1, end+1000)
		a.PublicID, b.PublicID = "log_page_tie_a", "log_page_tie_b"
		a.RequestTS = time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC).UnixMilli()
		if err := db.Create(&[]Log{a, b}).Error; err != nil {
			t.Fatal(err)
		}
		filter := ProjectLogFilter{Start: end + 999, End: end + 1001}
		logs, total, err := ListProjectLogsContext(context.Background(), owner, filter, 0, 10)
		if err != nil || total != 2 || len(logs) != 2 {
			t.Fatalf("duplicate ids lost: rows=%d total=%d err=%v", len(logs), total, err)
		}
		ids := []string{logs[0].PublicID, logs[1].PublicID}
		sort.Strings(ids)
		if !reflect.DeepEqual(ids, []string{a.PublicID, b.PublicID}) {
			t.Fatal("same-id physical rows were multiplied or collapsed")
		}
		// The existing contract only orders by id. It does not choose which of
		// two equal IDs appears at a cut through the tie; keep that unspecified.
		logs, total, err = ListProjectLogsContext(context.Background(), owner, filter, 1, 1)
		if err != nil || total != 2 || len(logs) != 1 || logs[0].Id != 21 {
			t.Fatal("tie pagination changed the exact count or row multiplicity")
		}
	})
	t.Run("canceled-count-and-page", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := ListProjectLogsContext(ctx, owner, base, 0, 5); !errors.Is(err, context.Canceled) {
			t.Fatalf("count lost request cancellation: %v", err)
		}
		if _, err := ListProjectLogPageContext(ctx, owner, base, 0, 5); !errors.Is(err, context.Canceled) {
			t.Fatalf("page lost request cancellation: %v", err)
		}
	})
}
