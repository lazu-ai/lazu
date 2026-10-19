package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRollbackCommandRejectsUnsupportedTargetBeforeOpeningStores(t *testing.T) {
	if os.Getenv("LAZU_ROLLBACK_TARGET_CHILD") == "1" {
		os.Args = []string{os.Args[0], "-command", "prepare-legacy-rollback", "-writers-stopped", "-legacy-schema-version", os.Getenv("LAZU_ROLLBACK_TARGET_VERSION")}
		main()
		return
	}
	for _, version := range []string{"0", "22", "26", "27"} {
		t.Run(version, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestRollbackCommandRejectsUnsupportedTargetBeforeOpeningStores$")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "LAZU_ROLLBACK_TARGET_CHILD=1", "LAZU_ROLLBACK_TARGET_VERSION="+version, "LAZU_MIGRATION_SOURCE_URL=", "APP_ENV=development")
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "unsupported legacy rollback schema "+version) || strings.Contains(string(output), "migration database URL") {
				t.Fatalf("rollback did not reject unsupported target before opening stores: %v\n%s", err, output)
			}
		})
	}
}

func TestRollbackCommandRejectsOlderSourceBeforeOpeningRedis(t *testing.T) {
	if os.Getenv("LAZU_ROLLBACK_SOURCE_CHILD") == "1" {
		os.Args = []string{os.Args[0], "-command", "prepare-legacy-rollback", "-writers-stopped", "-legacy-schema-version", "25"}
		main()
		return
	}
	source := schemaCheckFixture(t, "not-prepared-for-schema25", "primary", 23)
	cmd := exec.Command(os.Args[0], "-test.run=^TestRollbackCommandRejectsOlderSourceBeforeOpeningRedis$")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "LAZU_ROLLBACK_SOURCE_CHILD=1", "LAZU_MIGRATION_SOURCE_URL="+source, "REDIS_CONN_STRING=invalid", "APP_ENV=development")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "cannot mark primary schema 23 as legacy target 25") {
		t.Fatalf("rollback failed to reject older source before Redis/financial work: %v\n%s", err, output)
	}
}

func TestRollbackCommandRequiresStoppedWritersBeforeOpeningStores(t *testing.T) {
	if os.Getenv("LAZU_ROLLBACK_GUARD_CHILD") == "1" {
		os.Args = []string{os.Args[0], "-command", "prepare-legacy-rollback"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRollbackCommandRequiresStoppedWritersBeforeOpeningStores$")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "LAZU_ROLLBACK_GUARD_CHILD=1", "LAZU_MIGRATION_SOURCE_URL=", "APP_ENV=development")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "provide -writers-stopped") {
		t.Fatalf("rollback did not reject missing attestation first: %v\n%s", err, output)
	}
}
