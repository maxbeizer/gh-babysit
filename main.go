package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/maxbeizer/gh-babysit/internal/app"
	"github.com/maxbeizer/gh-babysit/internal/exitcode"
)

var version = "dev"

func run(ctx context.Context, args []string) int {
	cmd := app.NewCommand(version)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(ctx); err != nil {
		reportError(os.Stderr, err)
		var categorized *exitcode.Error
		if errors.As(err, &categorized) {
			return exitcode.Code(err)
		}
		if cause := context.Cause(ctx); cause != nil {
			return exitcode.Code(cause)
		}
		return exitcode.Code(exitcode.New(exitcode.Usage, err))
	}
	return exitcode.Code(context.Cause(ctx))
}

func reportError(out io.Writer, err error) {
	if !exitcode.IsReported(err) {
		fmt.Fprintf(out, "error: %v\n", err)
	}
}

func main() {
	ctx, cancel := context.WithCancelCause(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	go func() {
		sig := <-signals
		category := exitcode.SafetyStop
		if sig == syscall.SIGINT {
			category = exitcode.Interrupted
		}
		cancel(exitcode.New(category, fmt.Errorf("received signal %v", sig)))
	}()
	defer cancel(nil)
	os.Exit(run(ctx, os.Args[1:]))
}
