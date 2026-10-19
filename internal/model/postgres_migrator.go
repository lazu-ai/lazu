package model

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// safePostgresDialector keeps AutoMigrate from weakening a legacy unique
// index. GORM v1.25.7 reads PostgreSQL unique indexes as column uniqueness and
// then attempts to drop a generated constraint whenever the model uses
// uniqueIndex rather than unique. Older Lazu schemas commonly have the index
// but not that generated constraint name, so the drop aborts startup.
type safePostgresDialector struct {
	*postgres.Dialector
}

func (d safePostgresDialector) Migrator(db *gorm.DB) gorm.Migrator {
	return safePostgresMigrator{Migrator: d.Dialector.Migrator(db).(postgres.Migrator)}
}

type safePostgresMigrator struct {
	postgres.Migrator
}

func (m safePostgresMigrator) ColumnTypes(value interface{}) ([]gorm.ColumnType, error) {
	// The v1.5.2 driver injects pgx.QueryExecModeSimpleProtocol into Vars
	// before LIMIT is bound, producing $2 with only one SQL argument. The
	// connection already uses simple protocol, so suppress that workaround.
	patched := m.Migrator
	if dialector, ok := patched.Dialector.(postgres.Dialector); ok {
		patched.Dialector = safePostgresDialector{Dialector: &dialector}
	}
	return patched.ColumnTypes(value)
}

func (m safePostgresMigrator) HasTable(value interface{}) bool {
	var found, cached bool
	_ = m.RunWithValue(value, func(stmt *gorm.Statement) error {
		columns, ok := cachedSchemaObject(m.DB, stmt.Table)
		cached = ok
		found = columns != nil
		return nil
	})
	if cached {
		return found
	}
	return m.Migrator.HasTable(value)
}
func (m safePostgresMigrator) HasColumn(value interface{}, field string) bool {
	var found, cached bool
	_ = m.RunWithValue(value, func(stmt *gorm.Statement) error {
		columns, ok := cachedSchemaObject(m.DB, stmt.Table)
		cached = ok
		if stmt.Schema != nil {
			if f := stmt.Schema.LookUpField(field); f != nil {
				field = f.DBName
			}
		}
		found = columns[field]
		return nil
	})
	if cached {
		return found
	}
	return m.Migrator.HasColumn(value, field)
}

func (m safePostgresMigrator) MigrateColumn(value interface{}, field *schema.Field, columnType gorm.ColumnType) error {
	if unique, ok := columnType.Unique(); ok && unique && !field.Unique {
		// The existing database object is already unique. Preserve it instead
		// of letting GORM issue DROP CONSTRAINT for a name it did not create.
		fieldCopy := *field
		fieldCopy.Unique = true
		return m.Migrator.MigrateColumn(value, &fieldCopy, columnType)
	}
	return m.Migrator.MigrateColumn(value, field, columnType)
}
