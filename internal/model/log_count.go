package model

import "gorm.io/gorm"

// LogCountExactLimit bounds how many rows a console log list counts. Up to
// it the total is exact. Past it, an exact count would read every matching
// row of the range on each page view, so the count stops there and the page
// reports the limit as a lower bound.
var LogCountExactLimit = 10000 // a variable so tests can lower it

// countLogsCapped counts the rows of a log list query, reading at most
// LogCountExactLimit+1 of them. atLeast is true when more rows match than
// the limit; total is then LogCountExactLimit.
func countLogsCapped(query *gorm.DB) (total int64, atLeast bool, err error) {
	capped := query.Session(&gorm.Session{}).Model(&Log{}).Select("1").Limit(LogCountExactLimit + 1)
	var n int64
	// A new statement on the same connection and context: the outer count
	// must not inherit the list's conditions.
	err = query.Session(&gorm.Session{NewDB: true, Context: query.Statement.Context}).
		Table("(?) AS capped_logs", capped).
		Count(&n).Error
	if err != nil {
		return 0, false, err
	}
	if n > int64(LogCountExactLimit) {
		return int64(LogCountExactLimit), true, nil
	}
	return n, false, nil
}
