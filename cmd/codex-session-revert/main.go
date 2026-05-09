package main

import (
	"fmt"
	"os"

	"codex-session-revert/internal/app"
)

func main() {
	cli, err := app.NewApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\nNext: ensure HOME is set, then retry.\n", err)
		os.Exit(1)
	}
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(cli.Err, "Error: %v\n", err)
		os.Exit(1)
	}
}
