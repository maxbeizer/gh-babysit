package domain

import "testing"

func TestStateTransitions(t *testing.T) {
	tests := []struct {
		from, to State
		want     bool
	}{{StatePreflight, StateAwaitingSuite, true}, {StateWaitingRequired, StateDiagnosingFailure, true}, {StatePushing, StateAwaitingSuite, true}, {StateSuccess, StatePreflight, false}, {StatePreflight, StateCommitting, false}}
	for _, tt := range tests {
		if got := CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}
func TestCheckIdentityIncludesApp(t *testing.T) {
	a, b := int64(1), int64(2)
	x := CheckIdentity{Context: "build", AppID: &a}
	y := CheckIdentity{Context: "build", AppID: &b}
	if *x.AppID == *y.AppID {
		t.Fatal("different apps must remain distinguishable")
	}
}
