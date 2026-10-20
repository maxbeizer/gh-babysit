package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/maxbeizer/gh-babysit/internal/app"
	"github.com/maxbeizer/gh-babysit/internal/exitcode"
)

func TestReportErrorPrintsOnlyUnreportedErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "ordinary error", err: errors.New("broken"), want: "error: broken\n"},
		{name: "reported failure", err: exitcode.NewReported(exitcode.NotFixable, errors.New("broken"))},
		{name: "wrapped reported failure", err: errors.Join(errors.New("context"), exitcode.NewReported(exitcode.NotFixable, errors.New("broken")))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			reportError(&stderr, tt.err)
			if got := stderr.String(); got != tt.want {
				t.Fatalf("stderr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRootCommandVersion(t *testing.T) {
	var stdout bytes.Buffer
	cmd := app.NewCommand("v1.2.3")
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := stdout.String(), "gh-babysit version v1.2.3\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}
