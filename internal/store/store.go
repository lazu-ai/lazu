// Package store identifies each persistence boundary. Transactions always stay
// on one handle; passing a Core where Telemetry is required is a compile error.
package store

import (
	"gorm.io/gorm"
)

type Core struct{ db *gorm.DB }
type Telemetry struct{ db *gorm.DB }

func NewCore(db *gorm.DB) Core           { return Core{db: db} }
func NewTelemetry(db *gorm.DB) Telemetry { return Telemetry{db: db} }

func (s Core) SQL() *gorm.DB      { return s.db }
func (s Telemetry) SQL() *gorm.DB { return s.db }
