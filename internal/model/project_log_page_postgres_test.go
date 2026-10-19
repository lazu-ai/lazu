package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func projectLogPagePostgresFixture(t *testing.T) (*gorm.DB, ProjectLogAccess, int64, int64) {
	t.Helper()
	_, db := postgresTelemetryFixture(t)
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
	all := []int{17, 16, 15, 14, 13, 12, 11, 6, 5, 3, 2, 1}
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
		{"type", owner, ProjectLogFilter{Start: start, End: end, Type: LogTypeConsume}, []int{16, 14, 13, 12, 11, 6, 5, 3, 2, 1}},
		{"rejected-overrides-type", owner, ProjectLogFilter{Start: start, End: end, Type: LogTypeConsume, Rejected: true}, []int{17, 15}},
		{"effort", owner, ProjectLogFilter{Start: start, End: end, EffortClamped: true}, []int{16}},
		{"model", owner, ProjectLogFilter{Start: start, End: end, Model: "targ%"}, []int{16, 15}},
		{"token-name", owner, ProjectLogFilter{Start: start, End: end, TokenName: "member-key"}, []int{16, 15, 2}},
		{"group", owner, ProjectLogFilter{Start: start, End: end, Group: "target-group"}, []int{16, 15}},
		{"reason", owner, ProjectLogFilter{Start: start, End: end, Reason: "quota"}, []int{15}},
		{"unknown-reason", owner, ProjectLogFilter{Start: start, End: end, Reason: "unknown"}, []int{17, 16, 14, 13, 12, 11, 6, 5, 3, 2, 1}},
		{"request-keyword", owner, ProjectLogFilter{Start: start, End: end, Keyword: "reject-quota"}, []int{15}},
		{"content-keyword", owner, ProjectLogFilter{Start: start, End: end, Keyword: "denied"}, []int{15}},
		{"all-filters", member, ProjectLogFilter{Start: start, End: end, Type: LogTypeError, Model: "target", TokenName: "member-key", Group: "target-group", Reason: "quota", Keyword: "denied"}, []int{15}},
		{"inclusive-boundary", owner, ProjectLogFilter{Start: start, End: start}, []int{11}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var before []*Log
			if err := ProjectLogsQuery(test.access, test.filter).Order("logs.id DESC").Find(&before).Error; err != nil {
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

func TestPostgresProjectLogPageAvoidsHistoricalIDWalk(t *testing.T) {
	db, access, start, end := projectLogPagePostgresFixture(t)
	oldAt := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	if err := db.Exec(`INSERT INTO logs(id,public_id,request_ts,created_at,project_id,user_id,type,content)
SELECT i,'log_history_'||i,?, ?,42,1,2,repeat('history',50) FROM generate_series(1000,21000) i`, oldAt.UnixMilli(), oldAt.Unix()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO logs(id,public_id,request_ts,created_at,project_id,user_id,type,content)
SELECT i,'log_current_'||i,?, ?,CASE WHEN i <= 100 THEN 42 ELSE 43 END,1,2,repeat('current',50) FROM generate_series(1,2000) i`, start*1000, start).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ANALYZE logs").Error; err != nil {
		t.Fatal(err)
	}
	filter := ProjectLogFilter{Start: start, End: end}
	plan := func(query *gorm.DB) []map[string]interface{} {
		t.Helper()
		dry := query.Session(&gorm.Session{DryRun: true}).Find(&[]Log{})
		var raw string
		if err := db.Raw("EXPLAIN (ANALYZE,FORMAT JSON) "+dry.Statement.SQL.String(), dry.Statement.Vars...).Scan(&raw).Error; err != nil {
			t.Fatal(err)
		}
		var values []struct{ Plan map[string]interface{} }
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			t.Fatal(err)
		}
		var nodes []map[string]interface{}
		// Leaf nodes do not have a Plans key.
		var safeWalk func(map[string]interface{})
		safeWalk = func(node map[string]interface{}) {
			nodes = append(nodes, node)
			if children, ok := node["Plans"].([]interface{}); ok {
				for _, child := range children {
					safeWalk(child.(map[string]interface{}))
				}
			}
		}
		safeWalk(values[0].Plan)
		return nodes
	}
	old := plan(ProjectLogsQuery(access, filter).Order("logs.id DESC").Limit(5))
	badWalk := false
	for _, node := range old {
		removed, _ := node["Rows Removed by Filter"].(float64)
		if node["Relation Name"] == "logs_202609" && strings.Contains(fmt.Sprint(node["Index Name"]), "_id_") && removed >= 20000 {
			badWalk = true
		}
	}
	if !badWalk {
		t.Fatal("fixture did not reproduce the old LIMIT plan walking the empty historical leaf")
	}
	newPlan := plan(projectLogPageQuery(ProjectLogsQuery(access, filter), filter, 0, 5))
	for _, node := range newPlan {
		if node["Relation Name"] == "logs_202609" && strings.Contains(fmt.Sprint(node["Index Name"]), "_id_") {
			t.Fatal("filtered page still walks the historical id index before LIMIT")
		}
	}
	logs, total, err := ListProjectLogsContext(context.Background(), access, filter, 0, 5)
	if err != nil || total != 100 || len(logs) != 5 || logs[0].Id != 100 || logs[4].Id != 96 {
		t.Fatalf("filtered page/count changed: total=%d rows=%d err=%v", total, len(logs), err)
	}
	for _, test := range []struct {
		name   string
		filter ProjectLogFilter
	}{
		{"unwindowed-latest", ProjectLogFilter{}},
		{"start-only", ProjectLogFilter{Start: oldAt.Unix()}},
		{"end-only", ProjectLogFilter{End: end}},
		{"wide-dense-history", ProjectLogFilter{Start: oldAt.Unix(), End: end}},
	} {
		t.Run(test.name+"-keeps-index-path", func(t *testing.T) {
			nodes := plan(projectLogPageQuery(ProjectLogsQuery(access, test.filter), test.filter, 0, 5))
			indexed := false
			for _, node := range nodes {
				if node["Relation Name"] == "logs_202609" && strings.Contains(fmt.Sprint(node["Index Name"]), "_id_") {
					indexed = true
					if node["Actual Rows"].(float64) > 5 {
						t.Fatal("latest page unnecessarily read the complete historical leaf")
					}
				}
			}
			if !indexed {
				t.Fatal("unwindowed latest page lost its original id index path")
			}
			logs, err := ListProjectLogPageContext(context.Background(), access, test.filter, 0, 5)
			if err != nil || len(logs) != 5 || logs[0].Id != 21000 || logs[4].Id != 20996 {
				t.Fatalf("unwindowed latest page changed: rows=%d err=%v", len(logs), err)
			}
		})
	}
}
