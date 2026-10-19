package model

import (
	"database/sql"
	"strings"
	"sync"

	"gorm.io/gorm"
)

type schemaCatalogue struct{ columns map[string]map[string]bool }

var validatedSchemaCatalogues sync.Map // *sql.DB -> immutable catalogue
var validatedSchemaVersions sync.Map   // schemaValidationKey -> validated version

type schemaValidationKey struct {
	pool  *sql.DB
	scope string
}

func schemaVersionAlreadyValidated(db *gorm.DB, scope string) bool {
	if db.Dialector.Name() != "postgres" {
		return false // embedded fixtures can change their schema between calls
	}
	pool, err := db.DB()
	if err != nil {
		return false
	}
	_, ok := validatedSchemaVersions.Load(schemaValidationKey{pool, scope})
	return ok
}

func cacheValidatedSchemaVersion(db *gorm.DB, scope string) error {
	if err := refreshValidatedSchemaCatalogue(db); err != nil {
		return err
	}
	pool, err := db.DB()
	if err == nil && db.Dialector.Name() == "postgres" {
		validatedSchemaVersions.Store(schemaValidationKey{pool, scope}, SchemaVersionCurrent)
	}
	return err
}

// Schema validation checks the migration version first, then takes one
// catalogue snapshot. Runtime readiness checks never issue metadata SQL.
// Explicit migrations remove the snapshot before changing any schema.
func refreshValidatedSchemaCatalogue(db *gorm.DB) error {
	if db == nil || db.Dialector.Name() != "postgres" {
		return nil
	}
	raw, err := db.DB()
	if err != nil {
		return err
	}
	var rows []struct{ TableName, ColumnName string }
	if err := db.Raw("SELECT table_name,column_name FROM information_schema.columns WHERE table_schema = current_schema()").Scan(&rows).Error; err != nil {
		return err
	}
	result := &schemaCatalogue{columns: map[string]map[string]bool{}}
	for _, row := range rows {
		if result.columns[row.TableName] == nil {
			result.columns[row.TableName] = map[string]bool{}
		}
		result.columns[row.TableName][row.ColumnName] = true
	}
	validatedSchemaCatalogues.Store(raw, result)
	return nil
}
func invalidateSchemaCatalogue(db *gorm.DB) {
	if db == nil {
		return
	}
	if raw, err := db.DB(); err == nil {
		validatedSchemaCatalogues.Delete(raw)
		validatedSchemaVersions.Range(func(key, _ interface{}) bool {
			if key.(schemaValidationKey).pool == raw {
				validatedSchemaVersions.Delete(key)
			}
			return true
		})
	}
}
func cachedSchemaObject(db *gorm.DB, table string) (map[string]bool, bool) {
	raw, err := db.DB()
	if err != nil {
		return nil, false
	}
	value, ok := validatedSchemaCatalogues.Load(raw)
	if !ok {
		return nil, false
	}
	if parts := strings.Split(table, "."); len(parts) > 1 {
		table = parts[len(parts)-1]
	}
	return value.(*schemaCatalogue).columns[strings.Trim(table, `"`)], true
}
