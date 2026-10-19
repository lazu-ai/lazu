package model

import (
	"github.com/lazu-ai/lazu/internal/store"
	"gorm.io/gorm"
	"strings"
)

// InstallStoreOwnershipGuards runs only after both schemas are validated or
// migrated. Explicit migration connections stay outside this runtime guard.
func InstallStoreOwnershipGuards() error {
	if CoreDB() == nil || TelemetryDB() == nil || CoreDB() == TelemetryDB() {
		return nil
	}
	registry := store.ModelRegistry{}
	for _, binding := range []struct {
		db    *gorm.DB
		owner store.Boundary
	}{{CoreDB(), store.CoreBoundary}, {TelemetryDB(), store.TelemetryBoundary}} {
		tables, err := binding.db.Migrator().GetTables()
		if err != nil {
			return err
		}
		for _, table := range tables {
			if _, exists := registry[table]; !exists {
				registry[table] = binding.owner
			}
		}
	}
	for _, table := range TelemetryTables {
		registry[table] = store.TelemetryBoundary
	}
	for table := range registry {
		for _, parent := range []string{"logs", "routing_decision", "routing_outcome", "routing_attempt_outcome", "channel_probe_result"} {
			if strings.HasPrefix(table, parent+"_") {
				registry[table] = store.TelemetryBoundary
			}
		}
	}
	registry["schema_versions"] = "" // each database has its own lifecycle marker
	for _, binding := range []struct {
		db    *gorm.DB
		owner store.Boundary
	}{{CoreDB(), store.CoreBoundary}, {TelemetryDB(), store.TelemetryBoundary}} {
		if err := store.InstallModelGuard(binding.db, binding.owner, registry); err != nil {
			return err
		}
		if identity, ok := binding.db.ConnPool.(*identityConnPool); ok {
			// Leave identityTransaction outermost so commit-scoped mutation
			// fencing and external monetary debit callbacks keep their type.
			identity.ConnPool = store.GuardConnection(identity.ConnPool, binding.owner, registry)
		} else {
			pool := store.GuardConnection(binding.db.ConnPool, binding.owner, registry)
			binding.db.ConnPool, binding.db.Statement.ConnPool = pool, pool
		}
	}
	return nil
}
