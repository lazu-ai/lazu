package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"

	"gorm.io/gorm/schema"
)

// Populate every canonical column, with identifier/text fields at their schema
// limits and optional metrics present. No retired research payload is invented.
func populatedRoutingContract(t *testing.T, value interface{}, at time.Time) *schema.Schema {
	t.Helper()
	parsed, err := schema.Parse(value, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	root := reflect.ValueOf(value).Elem()
	for _, field := range parsed.Fields {
		v := root.FieldByName(field.Name)
		if field.Name == "IdempotencyKey" {
			continue
		} // writer dedupe credential, no reader JSON field
		if v.Kind() == reflect.Ptr {
			v.Set(reflect.New(v.Type().Elem()))
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.String:
			length := int(field.Size)
			if length == 0 {
				length = 32
			}
			text := ""
			for i := 0; len(text) < length; i++ {
				sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", field.Name, i)))
				text += hex.EncodeToString(sum[:])
			}
			v.SetString(text[:length])
		case reflect.Int, reflect.Int64:
			v.SetInt(1234)
		case reflect.Bool:
			v.SetBool(true)
		case reflect.Float64:
			v.SetFloat(0.12345678)
		case reflect.Struct:
			if v.Type() == reflect.TypeOf(at) {
				v.Set(reflect.ValueOf(at))
			}
		}
	}
	return parsed
}

func TestPostgresRoutingFactCanonicalContractAndSize(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	at := time.Now().UTC().Truncate(time.Second)
	decision, outcome := RoutingDecision{}, RoutingOutcome{}
	decisionSchema := populatedRoutingContract(t, &decision, at)
	outcomeSchema := populatedRoutingContract(t, &outcome, at)
	decision.RequestID = common.GenerateRequestID() // actual gateway ID contract, including its prefix
	outcome.RequestID, outcome.DecisionID, outcome.FinalGroup = decision.RequestID, decision.ID, ChannelLaneDirect
	status, retries := 429, 3
	outcome.StatusCode, outcome.RetryCount, outcome.FinishReason = &status, &retries, "length"
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	if err := persistRoutingSnapshot(decision.RequestID, at, &decision, &outcome); err != nil {
		t.Fatal(err)
	}
	for _, contract := range []struct {
		parsed   *schema.Schema
		original interface{}
		loaded   interface{}
		retired  []string
	}{
		{decisionSchema, &decision, &RoutingDecision{}, retiredRoutingDecisionColumns},
		{outcomeSchema, &outcome, &RoutingOutcome{}, retiredRoutingOutcomeColumns},
	} {
		if err := db.Where("request_id = ?", decision.RequestID).First(contract.loaded).Error; err != nil {
			t.Fatal(err)
		}
		loaded := reflect.ValueOf(contract.loaded).Elem()
		for _, field := range contract.parsed.Fields {
			v := loaded.FieldByName(field.Name)
			if v.Kind() == reflect.Ptr {
				if v.IsNil() {
					continue
				}
				v = v.Elem()
			}
			if v.Type() == reflect.TypeOf(at) {
				v.Set(reflect.ValueOf(v.Interface().(time.Time).UTC()))
			}
		}
		want, _ := json.Marshal(contract.original)
		got, _ := json.Marshal(contract.loaded)
		if string(want) != string(got) {
			t.Fatalf("%s omitted/changed canonical reader fields\nwant%s\ngot%s", contract.parsed.Table, want, got)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(got, &payload); err != nil {
			t.Fatal(err)
		}
		columns, err := db.Migrator().ColumnTypes(contract.parsed.Table)
		if err != nil {
			t.Fatal(err)
		}
		actual := map[string]bool{}
		for _, column := range columns {
			actual[column.Name()] = true
		}
		canonical := []string{}
		for _, field := range contract.parsed.Fields {
			if field.DBName != "" {
				canonical = append(canonical, field.DBName)
				if !actual[field.DBName] {
					t.Fatalf("reader column %s missing", field.DBName)
				}
			}
		}
		for _, retired := range contract.retired {
			if actual[retired] || payload[retired] != nil {
				t.Fatalf("retired research field %s retained", retired)
			}
		}
		t.Logf("%s canonical_columns=%d json_bytes=%d columns=%s", contract.parsed.Table, len(canonical), len(got), strings.Join(canonical, ","))
	}
	var sizes struct{ DecisionBytes, OutcomeBytes, DecisionStored, OutcomeStored int }
	if err := db.Raw("SELECT octet_length(routing_decision_json) AS decision_bytes, octet_length(routing_outcome_json) AS outcome_bytes, pg_column_size(routing_decision_json) AS decision_stored, pg_column_size(routing_outcome_json) AS outcome_stored FROM logs WHERE request_id = ?", decision.RequestID).Scan(&sizes).Error; err != nil {
		t.Fatal(err)
	}
	if sizes.DecisionBytes <= 0 || sizes.OutcomeBytes <= 0 || sizes.DecisionStored <= 0 || sizes.OutcomeStored <= 0 {
		t.Fatalf("no PostgreSQL size evidence: %+v", sizes)
	}
	t.Logf("fully_populated_schema_bound_fixture: decision_json_bytes=%d outcome_json_bytes=%d decision_pg_column_size=%d outcome_pg_column_size=%d; excludes indexes/heap overhead/retained historical archive", sizes.DecisionBytes, sizes.OutcomeBytes, sizes.DecisionStored, sizes.OutcomeStored)
}
