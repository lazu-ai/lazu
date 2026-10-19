package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"gorm.io/gorm"
)

type Boundary string

const (
	CoreBoundary      Boundary = "core"
	TelemetryBoundary Boundary = "telemetry"
)

// ModelRegistry is immutable once installed. It checks known physical table
// references in ORM and raw SQL; it is a runtime boundary, not a SQL compiler.
type ModelRegistry map[string]Boundary

func (registry ModelRegistry) CheckTable(boundary Boundary, table string) error {
	parts := strings.Split(strings.TrimSpace(table), ".")
	table = strings.Trim(parts[len(parts)-1], `"`)
	owner, exists := registry[table]
	if !exists {
		owner, exists = registry[strings.ToLower(table)]
	}
	if exists && owner != "" && owner != boundary {
		return fmt.Errorf("store ownership violation: %s belongs to %s, queried through %s", table, owner, boundary)
	}
	return nil
}

func (registry ModelRegistry) CheckSQL(boundary Boundary, query string) error {
	tokens := sqlTokens(query)
	for index, token := range tokens {
		switch strings.ToUpper(token) {
		case "FROM", "JOIN", "INTO", "UPDATE", "TABLE", "TRUNCATE", "COPY":
		default:
			continue
		}
		next := index + 1
		if next < len(tokens) && strings.EqualFold(tokens[next], "ONLY") {
			next++
		}
		if next >= len(tokens) || tokens[next] == "(" {
			continue
		}
		table := tokens[next]
		if next+2 < len(tokens) && tokens[next+1] == "." {
			table += "." + tokens[next+2]
		}
		if err := registry.CheckTable(boundary, table); err != nil {
			return err
		}
		if strings.EqualFold(token, "FROM") {
			depth := 0
			for cursor := next + 1; cursor < len(tokens); cursor++ {
				candidate := tokens[cursor]
				if candidate == "(" {
					depth++
					continue
				}
				if candidate == ")" {
					if depth == 0 {
						break
					}
					depth--
					continue
				}
				if depth != 0 {
					continue
				}
				switch strings.ToUpper(candidate) {
				case "WHERE", "GROUP", "HAVING", "ORDER", "LIMIT", "UNION", "RETURNING", "FOR", "JOIN", "ON", ";":
					cursor = len(tokens)
					continue
				}
				if candidate != "," || cursor+1 >= len(tokens) {
					continue
				}
				name := tokens[cursor+1]
				if cursor+3 < len(tokens) && tokens[cursor+2] == "." {
					name += "." + tokens[cursor+3]
				}
				if err := registry.CheckTable(boundary, name); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Ignore literals/comments so their contents cannot be mistaken for tables.
func sqlTokens(query string) []string {
	runes := []rune(query)
	out := []string{}
	for index := 0; index < len(runes); {
		value := runes[index]
		if unicode.IsSpace(value) {
			index++
			continue
		}
		if value == '\'' {
			index++
			for index < len(runes) {
				if runes[index] == '\'' {
					index++
					if index < len(runes) && runes[index] == '\'' {
						index++
						continue
					}
					break
				}
				index++
			}
			continue
		}
		if index+1 < len(runes) && value == '-' && runes[index+1] == '-' {
			for index < len(runes) && runes[index] != '\n' {
				index++
			}
			continue
		}
		if index+1 < len(runes) && value == '/' && runes[index+1] == '*' {
			index += 2
			for index+1 < len(runes) && !(runes[index] == '*' && runes[index+1] == '/') {
				index++
			}
			index += 2
			continue
		}
		start := index
		if value == '"' {
			index++
			for index < len(runes) && runes[index] != '"' {
				index++
			}
			if index < len(runes) {
				index++
			}
			out = append(out, strings.Trim(string(runes[start:index]), `"`))
		} else if unicode.IsLetter(value) || value == '_' {
			index++
			for index < len(runes) && (unicode.IsLetter(runes[index]) || unicode.IsDigit(runes[index]) || runes[index] == '_' || runes[index] == '$') {
				index++
			}
			out = append(out, string(runes[start:index]))
		} else {
			out = append(out, string(value))
			index++
		}
	}
	return out
}

type ownershipPool struct {
	gorm.ConnPool
	boundary Boundary
	registry ModelRegistry
	sqlDB    *sql.DB
}

func GuardConnection(pool gorm.ConnPool, boundary Boundary, registry ModelRegistry) gorm.ConnPool {
	if old, ok := pool.(*ownershipPool); ok && old.boundary == boundary {
		return old
	}
	var sqlDB *sql.DB
	if db, ok := pool.(*sql.DB); ok {
		sqlDB = db
	} else if getter, ok := pool.(gorm.GetDBConnector); ok {
		sqlDB, _ = getter.GetDBConn()
	}
	return &ownershipPool{ConnPool: pool, boundary: boundary, registry: registry, sqlDB: sqlDB}
}

func (pool *ownershipPool) GetDBConn() (*sql.DB, error) {
	if pool.sqlDB == nil {
		return nil, gorm.ErrInvalidDB
	}
	return pool.sqlDB, nil
}
func (pool *ownershipPool) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	if err := pool.registry.CheckSQL(pool.boundary, query); err != nil {
		return nil, err
	}
	return pool.ConnPool.PrepareContext(ctx, query)
}
func (pool *ownershipPool) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if err := pool.registry.CheckSQL(pool.boundary, query); err != nil {
		return nil, err
	}
	return pool.ConnPool.ExecContext(ctx, query, args...)
}
func (pool *ownershipPool) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	if err := pool.registry.CheckSQL(pool.boundary, query); err != nil {
		return nil, err
	}
	return pool.ConnPool.QueryContext(ctx, query, args...)
}
func (pool *ownershipPool) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	if pool.registry.CheckSQL(pool.boundary, query) != nil {
		// sql.Row cannot carry a custom error. A cancelled context prevents
		// any SQL execution; ORM callbacks retain the precise violation.
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return pool.ConnPool.QueryRowContext(cancelled, "SELECT 1")
	}
	return pool.ConnPool.QueryRowContext(ctx, query, args...)
}
func (pool *ownershipPool) BeginTx(ctx context.Context, options *sql.TxOptions) (gorm.ConnPool, error) {
	var transaction gorm.ConnPool
	var err error
	if beginner, ok := pool.ConnPool.(gorm.TxBeginner); ok {
		transaction, err = beginner.BeginTx(ctx, options)
	} else if beginner, ok := pool.ConnPool.(gorm.ConnPoolBeginner); ok {
		transaction, err = beginner.BeginTx(ctx, options)
	} else {
		return nil, gorm.ErrInvalidTransaction
	}
	if err != nil {
		return nil, err
	}
	return &ownershipTransaction{ownershipPool: &ownershipPool{ConnPool: transaction, boundary: pool.boundary, registry: pool.registry, sqlDB: pool.sqlDB}}, nil
}

type ownershipTransaction struct{ *ownershipPool }

func (pool *ownershipTransaction) Commit() error {
	if tx, ok := pool.ConnPool.(gorm.TxCommitter); ok {
		return tx.Commit()
	}
	return gorm.ErrInvalidTransaction
}
func (pool *ownershipTransaction) Rollback() error {
	if tx, ok := pool.ConnPool.(gorm.TxCommitter); ok {
		return tx.Rollback()
	}
	return gorm.ErrInvalidTransaction
}

func InstallModelGuard(db *gorm.DB, boundary Boundary, registry ModelRegistry) error {
	if db == nil {
		return errors.New("store database unavailable")
	}
	guard := func(tx *gorm.DB) {
		if tx.Error != nil {
			return
		}
		if err := registry.CheckTable(boundary, tx.Statement.Table); err != nil {
			tx.AddError(err)
			return
		}
		if tx.Statement.SQL.Len() > 0 {
			tx.AddError(registry.CheckSQL(boundary, tx.Statement.SQL.String()))
		}
	}
	name := "store:ownership:" + string(boundary)
	for _, register := range []func(string, func(*gorm.DB)) error{db.Callback().Create().Before("*").Register, db.Callback().Query().Before("*").Register, db.Callback().Update().Before("*").Register, db.Callback().Delete().Before("*").Register, db.Callback().Row().Before("*").Register, db.Callback().Raw().Before("*").Register} {
		if err := register(name, guard); err != nil {
			return err
		}
	}
	return nil
}
