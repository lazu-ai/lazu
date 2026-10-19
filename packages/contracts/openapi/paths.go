package openapi

import (
	_ "embed"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed lazu-api-reference.v1.yaml
var reference []byte

var (
	pathsOnce sync.Once
	paths     map[string]map[string]bool
	parameter = regexp.MustCompile(`\{[^/{}]+\}`)
)

// HasPublicOperation accepts only a method and path present in the bundled
// public API contract. Parameter names may differ, but path segments may not.
func HasPublicOperation(method, path string) bool {
	pathsOnce.Do(func() {
		var contract struct {
			Paths map[string]map[string]any `yaml:"paths"`
		}
		if yaml.Unmarshal(reference, &contract) != nil {
			return
		}
		paths = make(map[string]map[string]bool, len(contract.Paths))
		for specPath, operations := range contract.Paths {
			methods := map[string]bool{}
			for name := range operations {
				switch strings.ToUpper(name) {
				case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
					methods[strings.ToUpper(name)] = true
				}
			}
			paths[parameter.ReplaceAllString(specPath, "{}")] = methods
		}
	})
	return paths[parameter.ReplaceAllString(path, "{}")][strings.ToUpper(method)]
}
