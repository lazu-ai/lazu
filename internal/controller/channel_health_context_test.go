package controller

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

type channelHealthReadContextKey struct{}

// A file-backed database survives cancellation discarding a pooled SQLite
// connection. Both stores belong to this test; no external Redis/PG is read.
func setupChannelHealthContextDB(t *testing.T) *gorm.DB {
	t.Helper()
	WaitForChannelHealthRefreshes()
	core, telemetry := model.CoreDB(), model.ConfiguredTelemetryDB()
	sqlite, postgres, redis := common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	db, err := gorm.Open(gormsqlite.Open(filepath.Join(t.TempDir(), "channel-health.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = true, false, false
	model.EnsureColumnNamesInitialized()
	channelHealthCache.Lock()
	oldItems, oldBuilding := channelHealthCache.items, channelHealthCache.building
	channelHealthCache.items = map[string]channelHealthSnapshot{}
	channelHealthCache.building = map[string]*sync.Mutex{}
	channelHealthCache.Unlock()
	t.Cleanup(func() {
		WaitForChannelHealthRefreshes()
		model.SetCoreDB(core)
		model.SetTelemetryDB(telemetry)
		common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = sqlite, postgres, redis
		channelHealthCache.Lock()
		channelHealthCache.items, channelHealthCache.building = oldItems, oldBuilding
		channelHealthCache.Unlock()
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(
		&model.Channel{}, &model.Ability{}, &model.RoutingDecision{}, &model.RoutingOutcome{},
		&model.RoutingAttemptOutcome{}, &model.ChannelProbeResult{}, &model.ChannelProbeClaim{},
		&model.ChannelProbeBudget{}, &model.RouteHealthHourly{}, &model.RouteHealthRollupState{}, &model.RouteHealthState{},
	); err != nil {
		t.Fatal(err)
	}
	channel := model.Channel{Id: 811, PublicID: "ch_health_context_fixture", Key: "synthetic-fixture", Name: "health context fixture", Models: "context-health-model", Group: model.ChannelLaneDirect, Status: common.ChannelStatusEnabled}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Ability{Group: model.ChannelLaneDirect, Model: "context-health-model", ChannelId: channel.Id, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func observeChannelHealthReads(t *testing.T, db *gorm.DB, observe func(*gorm.DB)) func() {
	t.Helper()
	const name = "test:channel-health-caller-context"
	if err := db.Callback().Query().Before("gorm:query").Register(name, observe); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Row().Before("gorm:row").Register(name, observe); err != nil {
		t.Fatal(err)
	}
	return func() {
		_ = db.Callback().Query().Remove(name)
		_ = db.Callback().Row().Remove(name)
	}
}

func TestChannelHealthControllersCanceledBeforeRead(t *testing.T) {
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"candidates", func(ctx context.Context) error {
			_, err := SelectChannelProbeCandidatesContext(ctx, time.Now().UTC())
			return err
		}},
		{"sweep", func(ctx context.Context) error { _, err := RunChannelProbeSweep(ctx, time.Now().UTC()); return err }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			db := setupChannelHealthContextDB(t)
			t.Setenv("CHANNEL_PROBE_DAILY_BUDGET_USD", "8")
			reads := 0
			remove := observeChannelHealthReads(t, db, func(tx *gorm.DB) {
				if !tx.DryRun {
					reads++
				}
			})
			defer remove()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := test.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled controller must return cancellation, got %v", err)
			}
			if reads != 0 {
				t.Fatalf("pre-canceled controller dispatched %d reads", reads)
			}
		})
	}
}

func TestChannelProbeSelectionDeadlineInterruptsExecutingRead(t *testing.T) {
	db := setupChannelHealthContextDB(t)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), channelHealthReadContextKey{}, "selection-owned"), 25*time.Millisecond)
	defer cancel()
	intercepted := 0
	remove := observeChannelHealthReads(t, db, func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if !strings.Contains(sql, "WITH events AS") || tx.DryRun {
			return
		}
		intercepted++
		deadline, bounded := tx.Statement.Context.Deadline()
		if tx.Statement.Context.Value(channelHealthReadContextKey{}) != "selection-owned" || !bounded || time.Until(deadline) > time.Second {
			tx.AddError(errors.New("channel health read lost its caller deadline"))
			return
		}
		// Execute a real expensive read inside this driver's QueryContext
		// cancellation handler. No callback sleep or artificial timeout error.
		tx.Statement.SQL.Reset()
		tx.Statement.SQL.WriteString("WITH RECURSIVE slow(n) AS (VALUES(0) UNION ALL SELECT n+1 FROM slow WHERE n<1000000000) SELECT n FROM slow ORDER BY n DESC LIMIT 1; SELECT '' AS model_name")
		tx.Statement.Vars = nil
	})
	defer remove()
	started := time.Now()
	_, err := SelectChannelProbeCandidatesContext(ctx, time.Now().UTC())
	if !errors.Is(err, context.DeadlineExceeded) || intercepted != 1 {
		t.Fatalf("executing read must be canceled once: intercepted=%d err=%v", intercepted, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("canceled health selection kept running for %s", elapsed)
	}
}
