package model

import (
	"sync/atomic"

	"gorm.io/gorm"
)

// SQLStatementOps are the GORM callback chains counted by
// lazu_sql_statements_total, in exposition order.
var SQLStatementOps = [...]string{"create", "query", "update", "delete", "row", "raw"}

var sqlStatementCounts [len(SQLStatementOps)]atomic.Int64

// SQLStatementCounts returns how many statements each callback chain has run
// since the process started, in SQLStatementOps order. The relay's warm path
// should add none; a rising query count under load points at a cache miss.
func SQLStatementCounts() [len(SQLStatementOps)]int64 {
	var counts [len(SQLStatementOps)]int64
	for i := range sqlStatementCounts {
		counts[i] = sqlStatementCounts[i].Load()
	}
	return counts
}

func registerSQLStatementCounters(db *gorm.DB) error {
	callbacks := db.Callback()
	register := [...]func(string, func(*gorm.DB)) error{
		func(n string, f func(*gorm.DB)) error { return callbacks.Create().After("*").Register(n, f) },
		func(n string, f func(*gorm.DB)) error { return callbacks.Query().After("*").Register(n, f) },
		func(n string, f func(*gorm.DB)) error { return callbacks.Update().After("*").Register(n, f) },
		func(n string, f func(*gorm.DB)) error { return callbacks.Delete().After("*").Register(n, f) },
		func(n string, f func(*gorm.DB)) error { return callbacks.Row().After("*").Register(n, f) },
		func(n string, f func(*gorm.DB)) error { return callbacks.Raw().After("*").Register(n, f) },
	}
	for i, op := range SQLStatementOps {
		counter := &sqlStatementCounts[i]
		if err := register[i]("lazu:count_"+op, func(*gorm.DB) { counter.Add(1) }); err != nil {
			return err
		}
	}
	return nil
}
