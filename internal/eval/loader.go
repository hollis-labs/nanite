//go:build eval

package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadScenariosDir walks dir recursively and loads all *.yaml files as
// Scenario values. Returns an error on the first unparseable file.
func LoadScenariosDir(dir string) ([]Scenario, error) {
	var scenarios []Scenario
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var s Scenario
		if err := yaml.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if s.ID == "" {
			return fmt.Errorf("%s: scenario missing id field", path)
		}
		scenarios = append(scenarios, s)
		return nil
	})
	return scenarios, err
}
