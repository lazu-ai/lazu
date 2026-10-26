package service

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Outbound HTTP follows one policy (docs/DEPLOYMENT.md, Outbound
// connections): URLs that arrive in requests go through the SSRF-checked
// shared client; addresses an operator configures may be private and use
// their own client with an explicit timeout. net/http's default client fits
// neither: it has no timeout. This test fails on a new use of it in
// non-test code.
var defaultClientDebt = map[string]string{
	// lazu-ai/lazu#96 moves the new-channel model list to the relay client.
	"internal/controller/channel.go": "model list fetch",
}

var defaultClientUse = regexp.MustCompile(`http\.(?:DefaultClient|Get|Head|Post|PostForm)\b`)

func TestNoDefaultHTTPClientInProductionCode(t *testing.T) {
	root := filepath.Join("..", "..")
	var found []string
	for _, dir := range []string{"internal", "apps", "packages"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if info.Name() == "node_modules" || info.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if defaultClientUse.Match(body) {
				rel, _ := filepath.Rel(root, path)
				found = append(found, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	for _, path := range found {
		if _, ok := defaultClientDebt[path]; !ok {
			t.Errorf("%s uses net/http's default client, which has no timeout; build a client with one (docs/DEPLOYMENT.md, Outbound connections)", path)
		}
	}
}
