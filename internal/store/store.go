// Package store identifies each persistence boundary. Transactions always stay
// on one handle; passing a Core where Telemetry is required is a compile error.
package store

import (
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
)

type Core struct{ db *gorm.DB }
type Telemetry struct{ db *gorm.DB }
type Hot struct{ client *redis.Client }

func NewCore(db *gorm.DB) Core           { return Core{db: db} }
func NewTelemetry(db *gorm.DB) Telemetry { return Telemetry{db: db} }
func NewHot(client *redis.Client) Hot    { return Hot{client: client} }
func (s Core) SQL() *gorm.DB             { return s.db }
func (s Telemetry) SQL() *gorm.DB        { return s.db }
func (s Hot) Redis() *redis.Client       { return s.client }
