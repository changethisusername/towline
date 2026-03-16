package tooldef

import (
	_ "embed"
	"os"
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
