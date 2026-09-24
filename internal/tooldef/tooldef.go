package tooldef

import (
	_ "embed"
	"fmt"
	"os"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

//go:embed tools.yaml
var ToolsFile []byte

//go:embed towline-tools.yaml
var TowlineToolsFile []byte

func CreateToolsFileIfNotExists(path string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err = os.WriteFile(path, ToolsFile, 0644)
		if err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

func CreateTowlineToolsFileIfNotExists(path string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err = os.WriteFile(path, TowlineToolsFile, 0644)
		if err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

// EnsureToolsFile writes the embedded definitions to path if it does not
// exist, or upgrades it if its version is older than the embedded one (the
// previous file is kept at path+".bak", so local customizations aren't
// lost). It returns a short description of what happened.
func EnsureToolsFile(path string, embedded []byte) (string, error) {
	current, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.WriteFile(path, embedded, 0644); err != nil {
			return "", err
		}
		return "created", nil
	}
	if err != nil {
		return "", err
	}

	have, want := fileVersion(current), fileVersion(embedded)
	if !semver.IsValid(want) || (semver.IsValid(have) && semver.Compare(have, want) >= 0) {
		return "up to date", nil
	}

	if err := os.WriteFile(path+".bak", current, 0644); err != nil {
		return "", fmt.Errorf("failed to back up %s: %w", path, err)
	}
	if err := os.WriteFile(path, embedded, 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("upgraded from %s to %s (previous file saved as %s.bak)", have, want, path), nil
}

// fileVersion returns the top-level version field of a tools YAML file.
func fileVersion(data []byte) string {
	var v struct {
		Version string `yaml:"version"`
	}
	_ = yaml.Unmarshal(data, &v)
	return v.Version
}
