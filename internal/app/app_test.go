package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestParsePullRequestArgument(t *testing.T) {
	tests := []struct {
		value   string
		number  int
		url     string
		wantErr bool
	}{
		{value: "42", number: 42},
		{value: "https://github.example.com/owner/repo/pull/42", url: "https://github.example.com/owner/repo/pull/42"},
		{value: "0", wantErr: true},
		{value: "branch-name", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := ParsePullRequestArgument(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParsePullRequestArgument() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got.Number != tt.number || got.URL != tt.url {
				t.Fatalf("ParsePullRequestArgument() = %#v", got)
			}
		})
	}
}

func TestCommandAcceptsDocumentedSurface(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommand("dev")
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"123", "--auto", "--agent-command", "agent", "--poll-interval", "1s", "--appearance-timeout", "2s", "--timeout", "3s"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not implemented yet") {
		t.Fatalf("output = %q", out.String())
	}
}
func TestCommandRejectsExtraArguments(t *testing.T) {
	cmd := NewCommand("dev")
	cmd.SetArgs([]string{"1", "2"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected argument error")
	}
}
func TestHelpNamesCommand(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommand("dev")
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "gh-babysit [<pr-number-or-url>]") {
		t.Fatalf("help = %q", out.String())
	}
}
