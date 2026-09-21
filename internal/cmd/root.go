// Package cmd builds mastbench's command tree.
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/carlmjohnson/versioninfo"
	"github.com/mastmq/bench/internal/cmd/scenario"
	"github.com/urfave/cli/v3"
)

// Execute runs the command tree and exits non-zero on failure.
func Execute() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mastbench:", err)

		os.Exit(1)
	}
}

// run is separate from [Execute] so the deferred signal cleanup runs before
// the process exits.
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := &cli.Command{
		Name:     "mastbench",
		Usage:    "load and latency benchmarks for an MQTT broker",
		Version:  versioninfo.Short(),
		Commands: scenario.Commands(),
	}

	return root.Run(ctx, os.Args)
}
