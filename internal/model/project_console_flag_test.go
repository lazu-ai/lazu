package model

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

// Every other test of this switch sets PROJECTS_ENABLED, which exercises the
// escape hatch rather than the path production takes: the stored option an
// admin flips on the settings page. Without this, the option wiring could
// break in either direction and the whole suite would still pass.
func TestProjectsEnabledReadsTheStoredOption(t *testing.T) {
	previousEnabled := common.ProjectsEnabled
	previousAllowed := common.ProjectsAllowedUsers
	t.Cleanup(func() {
		common.ProjectsEnabled = previousEnabled
		common.ProjectsAllowedUsers = previousAllowed
	})
	// An empty allowlist is what keeps these cases away from the database:
	// the lookup only happens for a named user.
	common.ProjectsAllowedUsers = ""

	for _, tc := range []struct {
		env    string
		name   string
		option bool
		want   bool
	}{
		{name: "option on, no env", option: true, want: true},
		{name: "option off, no env", option: false, want: false},
		// The variable still wins where it is set, so a local run and an
		// env-configured deployment behave exactly as they did before.
		{name: "env false overrides the option", option: true, env: "false", want: false},
		{name: "env true overrides the option", option: false, env: "true", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common.ProjectsEnabled = tc.option
			if tc.env != "" {
				t.Setenv("PROJECTS_ENABLED", tc.env)
			} else {
				t.Setenv("PROJECTS_ENABLED", "")
			}
			if got := ProjectsEnabledForUser(1); got != tc.want {
				t.Fatalf("ProjectsEnabledForUser = %v, want %v", got, tc.want)
			}
		})
	}
}

// A user id is not a user: callers pass 0 for an unauthenticated request, and
// no switch should turn that into access.
func TestProjectsEnabledRejectsAbsentUser(t *testing.T) {
	previous := common.ProjectsEnabled
	t.Cleanup(func() { common.ProjectsEnabled = previous })
	common.ProjectsEnabled = true
	t.Setenv("PROJECTS_ENABLED", "")
	if ProjectsEnabledForUser(0) {
		t.Fatal("the switch granted access without a user")
	}
}
