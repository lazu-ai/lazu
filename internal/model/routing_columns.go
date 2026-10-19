package model

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm/schema"
)

// Routing facts travel on the request's own log row. The JSON copies
// (routing_decision_json / routing_outcome_json) keep the payload exactly as
// it was published; the typed columns below are what every reader filters,
// joins and aggregates on.
//
// Reading a fact out of the JSON at query time cost a jsonb parse per row for
// every row ever written, before any time filter could apply. On 2026-10-10 the
// health worker's candidate query crossed its five-second budget that way and
// every probe and catalog health refresh stopped. A filterable fact is a
// column; the JSON is a record, not an index.
//
// Field names, Go types and column tags mirror RoutingOutcome and
// RoutingDecision one for one (TestLogRoutingColumnsMirrorFacts enforces it),
// so copying a fact into its columns is a field-by-name walk and the
// compatibility views can project "ro_"/"rd_" + the fact's own column name.

const (
	routingOutcomeColumnPrefix  = "ro_"
	routingDecisionColumnPrefix = "rd_"
)

// LogRoutingOutcome holds a RoutingOutcome's fields as nullable columns. A
// NULL ro_request_id means the row carries no outcome.
type LogRoutingOutcome struct {
	ChannelName           *string `gorm:"type:text"`
	RequestID             *string `gorm:"type:text"`
	OutcomeAt             *time.Time
	DecisionID            *int64
	UserID                *int
	ProjectID             *int
	ModelName             *string `gorm:"type:text"`
	ChannelID             *int
	FinalGroup            *string `gorm:"type:text"`
	StatusCode            *int
	ErrorCode             *string `gorm:"type:text"`
	ErrorType             *string `gorm:"type:text"`
	ProviderAttempted     *bool
	HealthEligible        *bool
	HealthFailed          *bool
	IsRetry               *bool
	RetryCount            *int
	ActualInputTokens     *int
	CachedInputTokens     *int
	CacheWriteTokens      *int
	CacheWrite5mTokens    *int `gorm:"column:cache_write_5m_tokens"`
	CacheWrite1hTokens    *int `gorm:"column:cache_write_1h_tokens"`
	CacheMissTokens       *int
	ActualOutputTokens    *int
	ReasoningTokens       *int
	AudioInputTokens      *int
	AudioOutputTokens     *int
	ImageInputTokens      *int
	ImageOutputTokens     *int
	UpstreamCostUSD       *float64 `gorm:"type:float8"`
	ChargedCostUSD        *float64 `gorm:"type:float8"`
	GrossMarginUSD        *float64 `gorm:"type:float8"`
	TotalDurationMs       *int
	TTFTMs                *int `gorm:"column:ttft_ms"`
	AnswerTTFTMs          *int `gorm:"column:answer_ttft_ms"`
	MaxStallMs            *int
	FinishReason          *string `gorm:"type:text"`
	WasStreaming          *bool
	StreamAborted         *bool
	UserRetriedWithin5Min *bool `gorm:"column:user_retried_within_5min"`
	UserSwitchedModel     *bool
	CreatedAt             *time.Time `gorm:"autoCreateTime:false"`
}

// LogRoutingDecision holds a RoutingDecision's fields as nullable columns. A
// NULL rd_id means the row carries no decision.
type LogRoutingDecision struct {
	ID             *int64
	RequestID      *string `gorm:"type:text"`
	DecisionAt     *time.Time
	IsPrimary      *bool
	UserID         *int
	TokenID        *int
	SessionID      *string `gorm:"type:text"`
	RequestedModel *string `gorm:"type:text"`
	ResolvedModel  *string `gorm:"type:text"`
	PromptHash     *string `gorm:"type:text"`
	StrategyName   *string `gorm:"type:text"`
	ChoseChannel   *int
	ChoseModel     *string    `gorm:"type:text"`
	ChoseProvider  *string    `gorm:"type:text"`
	DecisionReason *string    `gorm:"type:text"`
	CreatedAt      *time.Time `gorm:"autoCreateTime:false"`
}

// routingFactField is one fact field with its column on the log row. JSON is
// the published key, which the backfill and the JSON copy share.
type routingFactField struct {
	source  int // field index in the fact struct
	target  int // field index in the column struct
	dbName  string
	column  string
	json    string
	omit    bool // omitempty: a zero value is absent, as in the JSON copy
	sqlType string
}

type routingFactLayout struct {
	table  string
	prefix string
	marker string // the column whose NULL means "no fact on this row"
	fields []routingFactField
	// excluded are fact columns that are never published (json:"-"); the
	// compatibility view projects them as typed NULLs.
	excluded []routingFactField
}

var (
	routingLayoutsOnce sync.Once
	routingOutcomeLay  routingFactLayout
	routingDecisionLay routingFactLayout
	routingLayoutErr   error
)

func routingLayouts() (routingFactLayout, routingFactLayout) {
	routingLayoutsOnce.Do(func() {
		routingOutcomeLay, routingLayoutErr = buildRoutingFactLayout(&RoutingOutcome{}, &LogRoutingOutcome{}, routingOutcomeColumnPrefix, "request_id")
		if routingLayoutErr != nil {
			return
		}
		routingDecisionLay, routingLayoutErr = buildRoutingFactLayout(&RoutingDecision{}, &LogRoutingDecision{}, routingDecisionColumnPrefix, "id")
	})
	if routingLayoutErr != nil {
		// The layouts are static Go types; a mismatch is a programming error
		// that TestLogRoutingColumnsMirrorFacts reports first.
		panic(routingLayoutErr)
	}
	return routingOutcomeLay, routingDecisionLay
}

func routingOutcomeLayout() routingFactLayout  { outcome, _ := routingLayouts(); return outcome }
func routingDecisionLayout() routingFactLayout { _, decision := routingLayouts(); return decision }

func buildRoutingFactLayout(fact interface{}, columns interface{}, prefix, marker string) (routingFactLayout, error) {
	parsed, err := schema.Parse(fact, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		return routingFactLayout{}, err
	}
	target, err := schema.Parse(columns, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		return routingFactLayout{}, err
	}
	targetType := reflect.TypeOf(columns).Elem()
	layout := routingFactLayout{table: parsed.Table, prefix: prefix, marker: prefix + marker}
	for _, field := range parsed.Fields {
		if field.DBName == "" {
			continue
		}
		jsonName, omit := routingJSONName(field.StructField)
		entry := routingFactField{
			source:  field.StructField.Index[0],
			dbName:  field.DBName,
			column:  prefix + field.DBName,
			json:    jsonName,
			omit:    omit,
			sqlType: routingSQLType(field.FieldType),
		}
		if jsonName == "-" {
			layout.excluded = append(layout.excluded, entry)
			continue
		}
		targetField, ok := targetType.FieldByName(field.Name)
		if !ok {
			return routingFactLayout{}, fmt.Errorf("%s has no column for %s.%s", targetType.Name(), parsed.Table, field.Name)
		}
		if got := target.LookUpField(field.Name); got == nil || got.DBName != field.DBName {
			return routingFactLayout{}, fmt.Errorf("%s.%s must use column %s", targetType.Name(), field.Name, field.DBName)
		}
		if !routingColumnTypeMatches(field.FieldType, targetField.Type) {
			return routingFactLayout{}, fmt.Errorf("%s.%s is %s, want a pointer to %s", targetType.Name(), field.Name, targetField.Type, field.FieldType)
		}
		entry.target = targetField.Index[0]
		layout.fields = append(layout.fields, entry)
	}
	for i := 0; i < targetType.NumField(); i++ {
		if _, ok := reflect.TypeOf(fact).Elem().FieldByName(targetType.Field(i).Name); !ok {
			return routingFactLayout{}, fmt.Errorf("%s.%s has no fact field", targetType.Name(), targetType.Field(i).Name)
		}
	}
	return layout, nil
}

func routingJSONName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	name, options, _ := strings.Cut(tag, ",")
	if name == "" {
		name = field.Name
	}
	return name, strings.Contains(","+options+",", ",omitempty,")
}

func routingColumnTypeMatches(fact reflect.Type, column reflect.Type) bool {
	if column.Kind() != reflect.Ptr {
		return false
	}
	base := fact
	if base.Kind() == reflect.Ptr {
		base = base.Elem()
	}
	return column.Elem() == base
}

// routingSQLType is the type the JSON-era views produced for a fact field,
// which the backfill casts to and the typed columns store.
func routingSQLType(t reflect.Type) string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint64:
		return "bigint"
	case reflect.Float32, reflect.Float64:
		return "double precision"
	case reflect.Struct:
		if t == reflect.TypeOf(time.Time{}) {
			return "timestamptz"
		}
	}
	return "text"
}

// fill copies a fact into its columns with the JSON copy's presence rules: a
// nil pointer, or a zero value under omitempty, is NULL.
func (layout routingFactLayout) fill(fact reflect.Value, columns reflect.Value) {
	for _, field := range layout.fields {
		value := fact.Field(field.source)
		target := columns.Field(field.target)
		if value.Kind() == reflect.Ptr {
			if value.IsNil() {
				target.Set(reflect.Zero(target.Type()))
				continue
			}
			value = value.Elem()
		} else if field.omit && value.IsZero() {
			target.Set(reflect.Zero(target.Type()))
			continue
		}
		copied := reflect.New(target.Type().Elem())
		copied.Elem().Set(value)
		target.Set(copied)
	}
}

// setRoutingOutcomeColumns stamps an outcome's typed columns on its log row.
func (log *Log) setRoutingOutcomeColumns(outcome *RoutingOutcome) {
	log.RoutingOutcome = LogRoutingOutcome{}
	if outcome == nil {
		return
	}
	routingOutcomeLayout().fill(reflect.ValueOf(outcome).Elem(), reflect.ValueOf(&log.RoutingOutcome).Elem())
}

// setRoutingDecisionColumns stamps a decision's typed columns on its log row.
func (log *Log) setRoutingDecisionColumns(decision *RoutingDecision) {
	log.RoutingDecision = LogRoutingDecision{}
	if decision == nil {
		return
	}
	routingDecisionLayout().fill(reflect.ValueOf(decision).Elem(), reflect.ValueOf(&log.RoutingDecision).Elem())
}

// routingOutcomeLogColumns are every log column an outcome writes: its JSON
// copy and its typed columns. Upserts that replace an outcome assign them all.
func routingOutcomeLogColumns() []string {
	layout := routingOutcomeLayout()
	columns := []string{"routing_outcome_json"}
	for _, field := range layout.fields {
		columns = append(columns, field.column)
	}
	return columns
}

// routingDecisionLogColumns are every log column a decision writes.
func routingDecisionLogColumns() []string {
	layout := routingDecisionLayout()
	columns := []string{"routing_decision_json"}
	for _, field := range layout.fields {
		columns = append(columns, field.column)
	}
	return columns
}

// routingFactColumnSet reports whether a log column belongs to a routing
// fact. Request-detail expiry keeps them: facts have their own retention.
func routingFactColumnSet() map[string]bool {
	set := map[string]bool{}
	for _, column := range routingOutcomeLogColumns() {
		set[column] = true
	}
	for _, column := range routingDecisionLogColumns() {
		set[column] = true
	}
	return set
}

// routingFactViewSQL is the PostgreSQL compatibility view for routing_outcome
// or routing_decision: the typed log columns, then the pre-cutover history no
// log row superseded. Both branches are plain projections of identical types
// with a simple filter, so PostgreSQL flattens the union and a caller's time,
// id or request filter, or a join on id, reaches each branch's index. A
// correlated "superseded by a log" check here would stop that flattening and
// compute the whole history for every join, so it is precomputed once into
// the legacy table's superseded column (markSupersededRoutingHistory); the
// legacy tables have taken no writes since the cutover.
func routingFactViewSQL(layout routingFactLayout, parsed *schema.Schema) string {
	current := make([]string, 0, len(parsed.Fields))
	history := make([]string, 0, len(parsed.Fields))
	byDB := map[string]routingFactField{}
	for _, field := range layout.fields {
		byDB[field.dbName] = field
	}
	excluded := map[string]routingFactField{}
	for _, field := range layout.excluded {
		excluded[field.dbName] = field
	}
	for _, field := range parsed.Fields {
		if field.DBName == "" {
			continue
		}
		sqlType := routingSQLType(field.FieldType)
		history = append(history, "old."+field.DBName+"::"+sqlType+" AS "+field.DBName)
		if column, ok := byDB[field.DBName]; ok {
			current = append(current, "logs."+column.column+"::"+sqlType+" AS "+field.DBName)
			continue
		}
		current = append(current, "NULL::"+excluded[field.DBName].sqlType+" AS "+field.DBName)
	}
	return fmt.Sprintf("CREATE VIEW %[1]s AS SELECT %[2]s FROM logs WHERE logs.%[3]s IS NOT NULL"+
		" UNION ALL SELECT %[4]s FROM %[1]s_legacy old WHERE NOT old.superseded",
		parsed.Table, strings.Join(current, ","), layout.marker, strings.Join(history, ","))
}

// routingFactBackfillSQL fills a request_ts range of log rows' typed columns
// from their JSON copies, with exactly the casts the JSON-era views applied.
// The row sub-select decodes each document once.
func routingFactBackfillSQL(layout routingFactLayout, jsonColumn string) string {
	columns := make([]string, 0, len(layout.fields))
	values := make([]string, 0, len(layout.fields))
	for _, field := range layout.fields {
		value := "(doc.j ->> '" + field.json + "')"
		if field.sqlType != "text" {
			value = "(" + value + ")::" + field.sqlType
		}
		columns = append(columns, field.column)
		values = append(values, value)
	}
	return "UPDATE logs SET (" + strings.Join(columns, ", ") + ") = (SELECT " + strings.Join(values, ", ") +
		" FROM (SELECT logs." + jsonColumn + "::jsonb AS j) doc)" +
		" WHERE logs.request_ts >= ? AND logs.request_ts < ? AND logs." + jsonColumn + " <> '' AND logs." + layout.marker + " IS NULL"
}
