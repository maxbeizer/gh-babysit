package exitcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{{"success", nil, 0}, {"usage", New(Usage, errors.New("bad")), 2}, {"declined", New(Declined, nil), 3}, {"not fixable", New(NotFixable, nil), 4}, {"safety", New(SafetyStop, nil), 5}, {"timeout", New(Timeout, nil), 6}, {"interrupt", fmt.Errorf("wrapped: %w", New(Interrupted, nil)), 130}, {"unknown", errors.New("oops"), 5}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Code(tt.err); got != tt.want {
				t.Fatalf("Code() = %d, want %d", got, tt.want)
			}
		})
	}
}
