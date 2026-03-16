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
		return runDestroy(args[1:])
	case "promote":
		return runPromote(args[1:])
	case "rotate-keys":
		return runRotateKeys(args[1:])
	case "status":
		return runStatus(args[1:])
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}
