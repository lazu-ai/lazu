package deprecations

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	deprecationLine = regexp.MustCompile(`Deprecated:\s*delete after\s+(\S+)`)
	releaseVersion  = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
	deletionDate    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	skippedDirs     = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, ".local-results": true}
)

type version [3]int

func parseVersion(text string) (version, error) {
	m := releaseVersion.FindStringSubmatch(strings.TrimRight(text, ".,;:)"))
	if m == nil {
		return version{}, fmt.Errorf("%q is not a vMAJOR.MINOR.PATCH release", text)
	}
	var v version
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, nil
}

func (v version) atOrBelow(o version) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] < o[i]
		}
	}
	return true
}

// findExpired returns one message per deprecation line in text that names a
// date before today, a release at or below current (when current is known),
// or neither a valid date nor a valid release.
func findExpired(name, text string, current *version, today time.Time) []string {
	var problems []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		m := deprecationLine.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		value := strings.TrimRight(m[1], ".,;:)")
		if deletionDate.MatchString(value) {
			due, err := time.Parse(time.DateOnly, value)
			switch {
			case err != nil:
				problems = append(problems, fmt.Sprintf("%s:%d: %q is not a valid date", name, line, value))
			case today.After(due):
				problems = append(problems, fmt.Sprintf("%s:%d: due for deletion after %s", name, line, value))
			}
			continue
		}
		due, err := parseVersion(m[1])
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s:%d: %q is neither a vMAJOR.MINOR.PATCH release nor a YYYY-MM-DD date", name, line, m[1]))
		case current != nil && due.atOrBelow(*current):
			problems = append(problems, fmt.Sprintf("%s:%d: due for deletion after %s", name, line, m[1]))
		}
	}
	return problems
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func TestNoExpiredDeprecations(t *testing.T) {
	var current *version
	if raw := strings.TrimSpace(os.Getenv("LAZU_CHECK_VERSION")); raw != "" {
		parsed, err := parseVersion(raw)
		if err != nil {
			t.Fatalf("LAZU_CHECK_VERSION: %v", err)
		}
		current = &parsed
	} else if os.Getenv("CI") == "true" {
		t.Fatal("LAZU_CHECK_VERSION must be set in CI (the newest release tag)")
	} else {
		t.Log("LAZU_CHECK_VERSION not set: release deadlines are not checked, dates are")
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	root := repoRoot(t)
	// This package's own docs and fixtures show the comment on purpose.
	self, _ := filepath.Abs(".")
	var problems []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] || path == self {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".ts", ".tsx":
		default:
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		problems = append(problems, findExpired(rel, string(body), current, today)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

func TestFindExpired(t *testing.T) {
	current := version{0, 7, 250}
	today := time.Date(2027, 1, 24, 0, 0, 0, 0, time.UTC)
	text := strings.Join([]string{
		"// Deprecated: delete after v0.7.249 (replaced by NewThing)",
		"// Deprecated: delete after v0.7.250.",
		"// Deprecated: delete after v0.7.251 (replaced by NewThing)",
		"// Deprecated: delete after v0.8.0",
		"// Deprecated: delete after next release",
		"// Deprecated: use NewThing instead.",
		"// Deprecated: delete after 2027-01-23 (setting retired)",
		"// Deprecated: delete after 2027-01-24.",
		"// Deprecated: delete after 2027-02-30",
	}, "\n")
	got := findExpired("x.go", text, &current, today)
	want := []string{
		"x.go:1: due for deletion after v0.7.249",
		"x.go:2: due for deletion after v0.7.250.",
		`x.go:5: "next" is neither a vMAJOR.MINOR.PATCH release nor a YYYY-MM-DD date`,
		"x.go:7: due for deletion after 2027-01-23",
		`x.go:9: "2027-02-30" is not a valid date`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Without a known release, release lines are left alone; dates still count.
	if got := findExpired("x.go", text, nil, today); len(got) != 3 {
		t.Fatalf("without a release: %v", got)
	}
}
