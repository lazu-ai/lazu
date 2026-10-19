package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm/schema"
)

// Every published routing fact field has a typed log column of the same name,
// so a new field cannot quietly become JSON-only and filterable by decoding.
func TestLogRoutingColumnsMirrorFacts(t *testing.T) {
	if _, err := buildRoutingFactLayout(&RoutingOutcome{}, &LogRoutingOutcome{}, routingOutcomeColumnPrefix, "request_id"); err != nil {
		t.Fatal(err)
	}
	if _, err := buildRoutingFactLayout(&RoutingDecision{}, &LogRoutingDecision{}, routingDecisionColumnPrefix, "id"); err != nil {
		t.Fatal(err)
	}
	logSchema, err := schema.Parse(&Log{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []routingFactLayout{routingOutcomeLayout(), routingDecisionLayout()} {
		for _, field := range layout.fields {
			if logSchema.LookUpField(field.column) == nil {
				t.Errorf("logs has no column %s", field.column)
			}
			if field.json != field.dbName {
				t.Errorf("%s publishes %q but stores %q; the backfill and views assume they match", layout.table, field.json, field.dbName)
			}
		}
	}
}

// A fact's typed columns hold exactly what its JSON copy publishes: a nil
// pointer or an omitted zero is NULL, everything else is its value.
func TestRoutingColumnsFollowJSONPresence(t *testing.T) {
	at := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	status, failed := 503, true
	outcome := &RoutingOutcome{RequestID: "req_1", OutcomeAt: at, DecisionID: 42, FinalGroup: "cheap", StatusCode: &status, HealthFailed: &failed}
	decision := &RoutingDecision{ID: 42, RequestID: "req_1", DecisionAt: at, IsPrimary: false, StrategyName: "baseline"}
	var row Log
	foldRelayRoutingIntoLog(&row, relayResultEvent{RoutingDecision: decision, RoutingOutcome: outcome})

	for _, check := range []struct {
		raw     string
		layout  routingFactLayout
		columns reflect.Value
	}{
		{row.RoutingOutcomeJSON, routingOutcomeLayout(), reflect.ValueOf(row.RoutingOutcome)},
		{row.RoutingDecisionJSON, routingDecisionLayout(), reflect.ValueOf(row.RoutingDecision)},
	} {
		var published map[string]json.RawMessage
		if err := json.Unmarshal([]byte(check.raw), &published); err != nil {
			t.Fatal(err)
		}
		for _, field := range check.layout.fields {
			column := check.columns.Field(field.target)
			raw, present := published[field.json]
			if present != !column.IsNil() {
				t.Errorf("%s: JSON present=%t, column set=%t", field.column, present, !column.IsNil())
				continue
			}
			if !present {
				continue
			}
			want := reflect.New(column.Type().Elem())
			if err := json.Unmarshal(raw, want.Interface()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want.Elem().Interface(), column.Elem().Interface()) {
				t.Errorf("%s = %v, JSON says %v", field.column, column.Elem().Interface(), want.Elem().Interface())
			}
		}
	}
	if row.RoutingOutcome.FinalGroup == nil || *row.RoutingOutcome.FinalGroup != "cheap" {
		t.Fatal("outcome lane was not stamped")
	}
	if row.RoutingDecision.IsPrimary == nil || *row.RoutingDecision.IsPrimary {
		t.Fatal("is_primary is always published, false included")
	}
}

// Readers filter routing facts by their typed columns. Decoding the JSON copy
// in a query costs a parse per retained row before any filter applies, which
// is how the health worker outgrew its budget on 2026-10-10. Only the
// documented writers below may touch the JSON copies as JSON.
func TestNoReaderDecodesRoutingJSON(t *testing.T) {
	allowed := map[string]string{
		"routing_columns.go":     "backfill from the JSON copy",
		"routing_queries.go":     "quality signals merged into the JSON copy",
		"telemetry_retention.go": "pre-partition copy without typed columns",
	}
	decode := regexp.MustCompile(`routing_(outcome|decision)_json\)?::jsonb`)
	root := filepath.Join("..", "..")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case "node_modules", ".git", ".local-results", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !decode.Match(body) {
			return nil
		}
		if _, ok := allowed[filepath.Base(path)]; ok && strings.HasSuffix(filepath.Dir(path), filepath.Join("internal", "model")) {
			return nil
		}
		t.Errorf("%s decodes a routing JSON copy in SQL; read the ro_*/rd_* log columns instead", path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
