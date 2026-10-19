package main

import (
	"bytes"
	"testing"
)

func TestRootCommandVersion(t *testing.T) {
	var stdout bytes.Buffer
	cmd := newRootCmd("v1.2.3")
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	const want = "gh-extension-template version v1.2.3\n"
	if got := stdout.String(); got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}
