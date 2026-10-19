package ledger

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSpoolFailureImmediatelyStopsNewAdmission(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(engine.SpoolDir, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	failed := false
	engine.SpoolFailure = func(error) { failed = true }
	engine.AdmissionCheck = func() error {
		if failed {
			return ErrUnavailable
		}
		return nil
	}
	if err := engine.PersistSettlement(testInput("accepted-before-disk-failure", 10), 10, "settle"); err == nil {
		t.Fatal("unwritable spool reported durable success")
	}
	if !failed {
		t.Fatal("disk failure did not fence subsequent admission")
	}
	if err := engine.Reserve(context.Background(), testInput("after-disk-failure", 1)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("admitted after durability failure: %v", err)
	}
}

func TestSpoolWriteProbeAndUsageLeaveNoProbeFiles(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = t.TempDir()
	if err := engine.CheckSpoolWritable(); err != nil {
		t.Fatal(err)
	}
	if err := engine.PersistSettlement(testInput("spool-usage", 10), 10, "settle"); err != nil {
		t.Fatal(err)
	}
	usage, err := engine.SpoolUsage()
	if err != nil || usage.Files != 1 || usage.Bytes == 0 || usage.FreeBytes == 0 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	entries, err := os.ReadDir(engine.SpoolDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("probe files leaked: %+v %v", entries, err)
	}
}

func TestCorruptedSpoolRetainsUnknownEvidenceWithoutDiskFailure(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = t.TempDir()
	path := filepath.Join(engine.SpoolDir, "accepted.json")
	if err := os.WriteFile(path, []byte(`{"broken"`), 0600); err != nil {
		t.Fatal(err)
	}
	failed := false
	engine.SpoolFailure = func(error) { failed = true }
	notified := false
	engine.SpoolQuarantine = func(record QuarantinedOperation) {
		notified = true
		if record.AmountKnown {
			t.Fatal("corrupt JSON was assigned a known monetary exposure")
		}
		if _, err := os.Stat(record.File + ".reason"); err != nil {
			t.Fatal("notification preceded durable quarantine metadata")
		}
	}
	if err := engine.ReplaySpool(context.Background()); err != nil || failed || !notified {
		t.Fatalf("corrupt accepted intent isolation failed: %v diskFailed=%v notified=%v", err, failed, notified)
	}
	if raw, err := os.ReadFile(filepath.Join(engine.SpoolDir, "quarantine", "accepted.json")); err != nil || string(raw) != `{"broken"` {
		t.Fatalf("corrupt evidence lost: %s %v", raw, err)
	}
}
