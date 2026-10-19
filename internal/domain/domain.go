package domain

import "time"

type PullRequest struct {
	Host, Repository                                     string
	Number                                               int
	URL, BaseBranch, HeadBranch, HeadRepository, HeadSHA string
}
type CheckSource string

const (
	CheckRun     CheckSource = "check_run"
	CommitStatus CheckSource = "commit_status"
)

type CheckIdentity struct {
	Context string
	AppID   *int64
}
type Check struct {
	Identity                                      CheckIdentity
	Name, URL, State, Conclusion, Workflow, Event string
	Source                                        CheckSource
	CheckRunID, WorkflowRunID                     *int64
	Attempt                                       int
	StartedAt, CompletedAt                        time.Time
	Required                                      bool
}
type CheckSnapshot struct {
	HeadSHA            string
	Required, Optional []Check
	ObservedAt         time.Time
}
type State string

const (
	StatePreflight           State = "preflight"
	StateAwaitingSuite       State = "awaiting-suite"
	StateWaitingRequired     State = "waiting-required"
	StateDiagnosingFailure   State = "diagnosing-failure"
	StateAwaitingEditConsent State = "awaiting-edit-consent"
	StateFixing              State = "fixing"
	StateValidating          State = "validating"
	StateAwaitingPushConsent State = "awaiting-push-consent"
	StateCommitting          State = "committing"
	StatePushing             State = "pushing"
	StateSuccess             State = "success"
	StateDeclined            State = "declined"
	StateNotFixable          State = "not-fixable"
	StateSafetyStop          State = "safety-stop"
	StateTimeout             State = "timeout"
)

var transitions = map[State]map[State]bool{
	StatePreflight:           {StateAwaitingSuite: true, StateSafetyStop: true},
	StateAwaitingSuite:       {StateWaitingRequired: true, StateSuccess: true, StateSafetyStop: true, StateTimeout: true},
	StateWaitingRequired:     {StateDiagnosingFailure: true, StateSuccess: true, StateSafetyStop: true, StateTimeout: true},
	StateDiagnosingFailure:   {StateAwaitingEditConsent: true, StateFixing: true, StateNotFixable: true, StateSafetyStop: true},
	StateAwaitingEditConsent: {StateFixing: true, StateDeclined: true}, StateFixing: {StateValidating: true, StateNotFixable: true, StateSafetyStop: true},
	StateValidating:          {StateAwaitingPushConsent: true, StateCommitting: true, StateNotFixable: true, StateSafetyStop: true},
	StateAwaitingPushConsent: {StateCommitting: true, StateDeclined: true}, StateCommitting: {StatePushing: true, StateSafetyStop: true},
	StatePushing: {StateAwaitingSuite: true, StateSafetyStop: true},
}

func CanTransition(from, to State) bool { return transitions[from][to] }
