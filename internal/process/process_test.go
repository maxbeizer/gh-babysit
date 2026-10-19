package process

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecRunnerBoundsOutputWhileSubprocessIsRunning(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "output.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 0123456789\nprintf abcdefghij >&2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := (ExecRunner{}).Run(context.Background(), Request{Name: script, StdoutLimit: 6, StderrLimit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout) > 6 || len(result.Stderr) > 4 || !result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("result=%+v", result)
	}
	if !strings.HasPrefix(string(result.Stdout), "012") || !strings.HasSuffix(string(result.Stdout), "789") {
		t.Fatalf("stdout=%q", result.Stdout)
	}
}
