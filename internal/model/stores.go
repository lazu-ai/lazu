package model

import (
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/store"
	"gorm.io/gorm"
)

// TypedStores names the persistence boundary of every operation. Accessors are
// kept in the model package for GORM's transaction helpers; application code
// should call model operations rather than joining tables from another store.
func CoreStore() store.Core { return store.NewCore(coreDB) }
func TelemetryStore() store.Telemetry {
	db := telemetryDB
	if db == nil && coreDB != nil && coreDB.Dialector.Name() == "sqlite" {
		db = coreDB
	}
	return store.NewTelemetry(db)
}
func HotStore() store.Hot   { return store.NewHot(common.RDB) }
func CoreDB() *gorm.DB      { return CoreStore().SQL() }
func TelemetryDB() *gorm.DB { return TelemetryStore().SQL() }

// TelemetryTables is the export/import allowlist. All remaining model tables
// belong to core. Historical routing facts remain telemetry until their
// backfill is complete; control-plane breaker state and probe claims stay core.
var TelemetryTables = []string{
	"logs", "routing_decision", "routing_outcome", "routing_attempt_outcome",
	"agent_sessions", "agent_session_threads", "agent_session_models", "agent_session_messages",
	"model_request_counters", "dashboard_usage_hourly_totals", "dashboard_usage_hourly_dimensions",
	"dashboard_usage_daily_dimensions", "digest_usage_hours", "usage_data",
	"dashboard_usage_hourly_slices", "channel_probe_result", "route_health_hourly", "route_health_rollup_state", "gateway_timing_daily", "mail_deliveries",
	"telemetry_event_receipts", "telemetry_channels", "telemetry_abilities",
	"telemetry_financial_receipts",
}

func telemetrySchemaModels() []interface{} {
	return append([]interface{}{
		&Log{}, &RoutingDecision{}, &RoutingOutcome{}, &RoutingAttemptOutcome{},
		&AgentSession{}, &AgentSessionThread{}, &AgentSessionModel{}, &AgentSessionMessage{},
		&ModelRequestCounter{}, &DashboardUsageHourlyTotal{}, &DashboardUsageHourlyDimension{},
		&DashboardUsageDailyDimension{}, &DigestUsageHour{}, &UsageData{}, &DashboardUsageHourlySlice{},
		&ChannelProbeResult{}, &RouteHealthHourly{}, &RouteHealthRollupState{}, &GatewayTimingDaily{}, &MailDelivery{}, &TelemetryEventReceipt{}, &TelemetryChannel{}, &TelemetryAbility{},
		&TelemetryFinancialReceipt{},
	}, extensions.telemetryModels...)
}

// Setters support isolated embedded test/runtime connections without public mutable handles.
func SetCoreDB(db *gorm.DB)      { coreDB = db }
func SetTelemetryDB(db *gorm.DB) { telemetryDB = db }

// ConfiguredTelemetryDB returns the explicit binding for lifecycle snapshots.
// Unlike TelemetryDB it does not resolve the embedded SQLite fallback.
func ConfiguredTelemetryDB() *gorm.DB { return telemetryDB }

func TelemetryChannelTable() string {
	if TelemetryDB() == CoreDB() {
		return "channels"
	}
	return "telemetry_channels"
}

// TelemetrySchemaModels is the registry used by explicit migration commands and
// embedded test installations. Production startups validate the migrated schema.
func TelemetrySchemaModels() []interface{} { return telemetrySchemaModels() }
