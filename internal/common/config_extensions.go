package common

import "gopkg.in/yaml.v3"

// ConfigSection is a top-level config.yaml section an extension reads for
// itself. Apply runs when the file has the section; decode fills a struct
// from it. Env runs after the file is merged, with the core's environment
// overrides, so environment variables win.
type ConfigSection struct {
	Name  string
	Apply func(decode func(target any) error)
	Env   func()
}

var configSections []ConfigSection

// RegisterConfigSection adds a section; register from init().
func RegisterConfigSection(section ConfigSection) {
	configSections = append(configSections, section)
}

func applyExtensionConfigSections(raw map[string]any) {
	for _, section := range configSections {
		if section.Apply == nil {
			continue
		}
		values, ok := yamlSection(raw, section.Name)
		if !ok {
			continue
		}
		section.Apply(func(target any) error {
			encoded, err := yaml.Marshal(values)
			if err != nil {
				return err
			}
			return yaml.Unmarshal(encoded, target)
		})
	}
}

func applyExtensionConfigEnvironment() {
	for _, section := range configSections {
		if section.Env != nil {
			section.Env()
		}
	}
}
