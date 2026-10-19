package main

import (
	"bytes"
	"testing"

	"github.com/maxbeizer/gh-babysit/internal/app"
)

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
