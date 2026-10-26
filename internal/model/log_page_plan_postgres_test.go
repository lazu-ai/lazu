package model

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

// explainLogPage runs EXPLAIN ANALYZE on a query and returns every plan node.
func explainLogPage(t *testing.T, db *gorm.DB, query *gorm.DB) []map[string]interface{} {
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
	var walk func(map[string]interface{})
	walk = func(node map[string]interface{}) {
		nodes = append(nodes, node)
		if children, ok := node["Plans"].([]interface{}); ok {
			for _, child := range children {
				walk(child.(map[string]interface{}))
			}
		}
	}
	walk(values[0].Plan)
	return nodes
}

// logRowsRead is the most rows any one table scan touched: the rows it
// returned plus those its filter threw away.
func logRowsRead(nodes []map[string]interface{}) float64 {
	most := 0.0
	for _, node := range nodes {
		if _, ok := node["Relation Name"]; !ok {
			continue
		}
		returned, _ := node["Actual Rows"].(float64)
		loops, _ := node["Actual Loops"].(float64)
		if loops < 1 {
			loops = 1
		}
		removed, _ := node["Rows Removed by Filter"].(float64)
		removedByIndex, _ := node["Rows Removed by Index Recheck"].(float64)
		if read := returned*loops + removed + removedByIndex; read > most {
			most = read
		}
	}
	return most
}

// The 2026-10-23 slow query, and every other log list over the same data: a
// project with few rows in a window longer than a day, while another project
// wrote newer rows and the previous month is full. Ordered by id, a five-row
// page walked the id index and threw away every other project's row; ordered
// newest first through a (filter, created_at) index, each list reads only its
// page, however wide the window or sparse the filter.
func TestPostgresLogPagesReadOnlyTheirPage(t *testing.T) {
	db, sparse, start, _ := projectLogPagePostgresFixture(t)
	history := time.Unix(start, 0).UTC().AddDate(0, -1, 14).Unix() // mid-month, the month before
	insert := func(from, to int, at int64, project, user, channel string) {
		t.Helper()
		sql := fmt.Sprintf(`INSERT INTO logs(id,public_id,request_ts,created_at,project_id,user_id,token_id,channel_id,type,content)
SELECT i,'log_plan_'||i, (?::bigint + i %% 86400) * 1000, ?::bigint + i %% 86400, %s, %s, %s, %s, 2, repeat('x',100) FROM generate_series(%d,%d) i`, project, user, user, channel, from, to)
		if err := db.Exec(sql, at, at).Error; err != nil {
			t.Fatal(err)
		}
	}
	insert(1, 20000, history, "43", "9", "3")                                         // the previous month, another project
	insert(20001, 20030, start, "42", "CASE WHEN i % 2 = 0 THEN 1 ELSE 2 END", "1")   // the sparse project
	insert(20031, 60000, start, "CASE WHEN i % 40 = 0 THEN 44 ELSE 43 END", "9", "3") // newer rows of other projects, one in forty for 44
	if err := db.Exec("UPDATE logs SET request_id = 'req-plan-sparse' WHERE id = 20005").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ANALYZE logs").Error; err != nil {
		t.Fatal(err)
	}
	days := ProjectLogFilter{Start: start - 86400, End: start + 2*86400}
	month := ProjectLogFilter{Start: history - 86400, End: start + 2*86400}
	dense := ProjectLogAccess{Project: Project{ID: 43, OwnerUserID: 9}, Viewer: ProjectMember{ProjectID: 43, UserID: 9, Role: ProjectRoleOwner, Status: ProjectMemberActive}}
	medium := ProjectLogAccess{Project: Project{ID: 44, OwnerUserID: 9}, Viewer: ProjectMember{ProjectID: 44, UserID: 9, Role: ProjectRoleOwner, Status: ProjectMemberActive}}
	member := sparse
	member.Viewer.UserID, member.Viewer.Role = 2, ProjectRoleMember

	// The statement that was slow: the owner-fallback scope, ordered by id,
	// with the indexes the partitioned table had before schema 37.
	if err := db.Exec("DROP INDEX logs_project_created").Error; err != nil {
		t.Fatal(err)
	}
	old := explainLogPage(t, db, db.Model(&Log{}).
		Where("logs.type <> ? AND (logs.project_id = ? OR ((logs.project_id = 0 OR logs.project_id IS NULL) AND logs.user_id = ?)) AND logs.created_at >= ? AND logs.created_at <= ?", LogTypeUnknown, 42, 1, days.Start, days.End).
		Order("logs.id DESC").Limit(5))
	if read := logRowsRead(old); read < 10000 {
		t.Fatalf("fixture no longer reproduces the id walk: it read %.0f rows", read)
	}
	if err := ensureLogListIndexes(db, false); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		query *gorm.DB
		limit float64
	}{
		{"project-sparse-several-days", projectLogPageQuery(ProjectLogsQuery(sparse, days), 0, 5), 5},
		{"project-sparse-one-day", projectLogPageQuery(ProjectLogsQuery(sparse, ProjectLogFilter{Start: start, End: start + 86400}), 0, 5), 5},
		{"project-sparse-unwindowed", projectLogPageQuery(ProjectLogsQuery(sparse, ProjectLogFilter{}), 0, 5), 5},
		{"project-sparse-third-page", projectLogPageQuery(ProjectLogsQuery(sparse, days), 10, 5), 15},
		{"project-member", projectLogPageQuery(ProjectLogsQuery(member, days), 0, 5), 5},
		{"project-dense-month", projectLogPageQuery(ProjectLogsQuery(dense, month), 0, 5), 5},
		// A thousand rows among forty thousand: too many to read and sort,
		// too few to find quickly in id order.
		{"project-thousand-rows-month", projectLogPageQuery(ProjectLogsQuery(medium, month), 0, 5), 5},
		{"project-request-id", orderLogsNewestFirst(ProjectLogsQuery(sparse, ProjectLogFilter{}).Where("logs.request_id = ?", "req-plan-sparse")).Limit(100), 1},
		{"admin-unfiltered-month", orderLogsNewestFirst(allLogsQuery(0, month.Start, month.End, "", "", 0, "", 0, "")).Limit(5), 5},
		{"admin-sparse-user-month", orderLogsNewestFirst(allLogsQuery(0, month.Start, month.End, "", "", 1, "", 0, "")).Limit(5), 5},
		{"admin-sparse-channel-month", orderLogsNewestFirst(allLogsQuery(0, month.Start, month.End, "", "", 0, "", 1, "")).Limit(5), 5},
		{"token-sparse-month", orderLogsNewestFirst(tokenConsumeLogsQuery(1, month.Start, month.End, TokenConsumeLogFilter{})).Limit(5), 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			// A scan may read a row past the page, skip a member's neighbours,
			// or read all 30 rows of a sparse filter and sort them. It may
			// not read a thousand-row project or the 20 000 to 40 000 rows of
			// a month or of another project.
			if read := logRowsRead(explainLogPage(t, db, test.query)); read > 64 {
				t.Fatalf("read %.0f rows from one partition for a page of %.0f", read, test.limit)
			}
		})
	}
	logs, total, err := ListProjectLogsContext(context.Background(), sparse, days, 0, 5)
	if err != nil || total != 30 || len(logs) != 5 || logs[0].Id != 20030 {
		t.Fatalf("project page: rows=%d total=%d err=%v", len(logs), total, err)
	}
	found, err := SearchProjectLogs(context.Background(), sparse, ProjectLogFilter{Keyword: "req-plan-sparse"})
	if err != nil || len(found) != 1 || found[0].Id != 20005 {
		t.Fatalf("request id search: %d rows, %v", len(found), err)
	}
	admin, total, _, err := GetAllLogs(0, month.Start, month.End, "", "", 1, "", 0, 5, 0, "")
	if err != nil || total != 15 || len(admin) != 5 || admin[0].UserId != 1 {
		t.Fatalf("admin user page: rows=%d total=%d err=%v", len(admin), total, err)
	}

	// The console's count stops at LogCountExactLimit: a dense month reads
	// at most one row past the limit instead of all of them.
	if read := logRowsRead(explainLogPage(t, db, ProjectLogsQuery(dense, month).Select("1").Limit(LogCountExactLimit+1))); read > float64(LogCountExactLimit+1) {
		t.Fatalf("capped count read %.0f rows", read)
	}
	if _, total, atLeast, err := ListProjectLogsCappedContext(context.Background(), dense, month, 0, 5); err != nil || total != int64(LogCountExactLimit) || !atLeast {
		t.Fatalf("dense project count: total=%d atLeast=%t err=%v", total, atLeast, err)
	}
	if _, total, atLeast, err := ListProjectLogsCappedContext(context.Background(), sparse, days, 0, 5); err != nil || total != 30 || atLeast {
		t.Fatalf("sparse project count: total=%d atLeast=%t err=%v", total, atLeast, err)
	}
	if _, total, atLeast, err := GetAllLogs(0, month.Start, month.End, "", "", 0, "", 0, 5, 0, ""); err != nil || total != int64(LogCountExactLimit) || !atLeast {
		t.Fatalf("admin month count: total=%d atLeast=%t err=%v", total, atLeast, err)
	}
}
