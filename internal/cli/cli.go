package cli

import (
	"fmt"
)

func Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: towline <command> [args]\nCommands: setup, init, list, destroy, promote, rotate-keys, status")
	}

	switch args[0] {
	case "setup":
		return runSetup(args[1:])
	case "init":
		return runInit(args[1:])
	case "list":
		return runList(args[1:])
	case "destroy":
		return fmt.Errorf("not yet implemented")
	case "promote":
		return fmt.Errorf("not yet implemented")
	case "rotate-keys":
		return fmt.Errorf("not yet implemented")
	case "status":
		return fmt.Errorf("not yet implemented")
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}
