package main

import (
	"fmt"
	"os"

	"github.com/changethisusername/towline/internal/cli"
)

var (
	Version   string
	BuildDate string
	Commit    string
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "towline %s (%s)\n", Version, Commit)
		fmt.Fprintf(os.Stderr, "Usage: towline <command> [args]\n")
		fmt.Fprintf(os.Stderr, "Commands: setup, init, list, destroy, promote, rotate-keys, status\n")
		os.Exit(1)
	}

	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}
