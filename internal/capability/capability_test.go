package capability

import (
	"context"
	"errors"
	"github.com/maxbeizer/gh-babysit/internal/process"
	"strings"
	"testing"
)

type fakeRunner struct {
	results  []process.Result
	errors   []error
	requests []process.Request
}

func (f *fakeRunner) Run(_ context.Context, r process.Request) (process.Result, error) {
	f.requests = append(f.requests, r)
	i := len(f.requests) - 1
	var e error
	if i < len(f.errors) {
		e = f.errors[i]
	}
	return f.results[i], e
}
func TestParseVersion(t *testing.T) {
	v, err := ParseVersion("gh version 2.80.1 (date)")
	if err != nil || v != (Version{2, 80, 1}) {
		t.Fatalf("ParseVersion = %#v, %v", v, err)
	}
}
func TestRequireChecksVersionAndCapability(t *testing.T) {
	f := &fakeRunner{results: []process.Result{{Stdout: []byte("tool 2.1.0")}, {}}, errors: []error{nil, nil}}
	if err := Require(context.Background(), f, "tool", Version{2, 0, 0}, "feature", "--help"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.requests[1].Args, " "); got != "feature --help" {
		t.Fatalf("probe = %q", got)
	}
}
func TestRequireRejectsOldVersion(t *testing.T) {
	f := &fakeRunner{results: []process.Result{{Stdout: []byte("tool 1.9.0")}}}
	if err := Require(context.Background(), f, "tool", Version{2, 0, 0}); err == nil {
		t.Fatal("expected error")
	}
}
func TestRequireRejectsMissingCapability(t *testing.T) {
	f := &fakeRunner{results: []process.Result{{Stdout: []byte("tool 2.0.0")}, {}}, errors: []error{nil, errors.New("unknown flag")}}
	if err := Require(context.Background(), f, "tool", Version{2, 0, 0}, "x"); err == nil {
		t.Fatal("expected error")
	}
}
