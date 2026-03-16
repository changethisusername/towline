package main

import (
	"fmt"
	"os"
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
	fmt.Fprintf(os.Stderr, "Command '%s' not yet implemented\n", os.Args[1])
	os.Exit(1)
}
