package model

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const requestLogTargetContextKey = "relay_request_log_target"
const provisionalLogTargetKey = "_relay_provisional_target"
const provisionalLogTargetJSON = `{"_relay_provisional_target":true}`

// MarkRequestLogTarget records that this request has captured or durably
// published a log fact. Diagnostics may arrive before that fact is consumed.
// The marker survives clearing the combined-event context after publication.
func MarkRequestLogTarget(c *gin.Context, requestID string) {
	if c != nil && requestID != "" && requestID == c.GetString(common.RequestIdKey) {
		c.Set(requestLogTargetContextKey, requestID)
	}
}

// HasRequestLogTarget is a request-local check; it does not look up a log in
// SQL or Redis. Authentication and public endpoints have no target by default.
func HasRequestLogTarget(c *gin.Context) bool {
	if c == nil {
		return false
	}
	requestID := c.GetString(common.RequestIdKey)
	return requestID != "" && c.GetString(requestLogTargetContextKey) == requestID
}

func markRelayResultLogTarget(c *gin.Context, event relayResultEvent) {
	if event.ConsumeLog != nil {
		MarkRequestLogTarget(c, event.ConsumeLog.RequestID)
	} else if event.RoutingOutcome != nil {
		MarkRequestLogTarget(c, event.RoutingOutcome.RequestID)
	}
}

// Only a newly inserted media carrier has this marker. Snapshot conflict
// updates cannot restore it after retention removed the original details.
func provisionalLogTargetSQL(column string) string {
	return "COALESCE(COALESCE(NULLIF(" + column + ",''),'{}')::jsonb -> '" + provisionalLogTargetKey + "', 'false'::jsonb) = 'true'::jsonb"
}

func hasProvisionalLogTarget(other string) bool {
	var fields map[string]interface{}
	return json.Unmarshal([]byte(other), &fields) == nil && fields[provisionalLogTargetKey] == true
}

func relayLogConflictClause(tx *gorm.DB) (clause.OnConflict, error) {
	conflict := clause.OnConflict{Columns: []clause.Column{{Name: "request_ts"}, {Name: "public_id"}}, UpdateAll: true}
	if tx.Dialector.Name() != "postgres" {
		return conflict, nil
	}
	statement := &gorm.Statement{DB: tx}
	if err := statement.Parse(&Log{}); err != nil {
		return conflict, err
	}
	// Preserve UpdateAll's writable/default/create-time rules while replacing
	// only Other's assignment. Final facts keep a provisional carrier's timing
	// and remove its internal marker; incoming canonical metadata wins.
	columns := make([]string, 0, len(statement.Schema.Fields))
	for _, field := range statement.Schema.Fields {
		if field.DBName != "" && !field.PrimaryKey && field.AutoCreateTime == 0 &&
			(!field.HasDefaultValue || field.DefaultValueInterface != nil || strings.EqualFold(field.DefaultValue, "NULL")) {
			columns = append(columns, field.DBName)
		}
	}
	conflict.UpdateAll = false
	conflict.DoUpdates = clause.AssignmentColumns(columns)
	for index := range conflict.DoUpdates {
		if conflict.DoUpdates[index].Column.Name == "other" {
			conflict.DoUpdates[index].Value = gorm.Expr("CASE WHEN logs.type = 0 AND (" + provisionalLogTargetSQL("logs.other") + ") THEN ((COALESCE(NULLIF(logs.other,''),'{}')::jsonb - '" + provisionalLogTargetKey + "') || COALESCE(NULLIF(excluded.other,''),'{}')::jsonb)::text ELSE excluded.other END")
		}
	}
	return conflict, nil
}
