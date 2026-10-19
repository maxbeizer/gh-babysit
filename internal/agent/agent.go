package agent

import (
	"context"
	"github.com/maxbeizer/gh-babysit/internal/domain"
)

type Mode string

const (
	Diagnose Mode = "diagnose"
	Fix      Mode = "fix"
)

type Classification string

const (
	CodeFixable           Classification = "code_fixable"
	FlakyOrInfrastructure Classification = "flaky_or_infrastructure"
	Ambiguous             Classification = "ambiguous"
	InsufficientEvidence  Classification = "insufficient_evidence"
)

type Confidence string

const (
	High   Confidence = "high"
	Medium Confidence = "medium"
	Low    Confidence = "low"
)

type Request struct {
	SchemaVersion               int
	Workspace                   string
	PullRequest                 domain.PullRequest
	RecordedSHA                 string
	FailedCheck                 domain.Check
	Mode                        Mode
	PriorDiagnosis, CurrentDiff string
	LogPaths                    []string
	Constraints                 []string
}
type Outcome struct {
	SchemaVersion                           int
	Classification                          Classification
	ClassificationConfidence, FixConfidence Confidence
	Diagnosis                               string
	Plan, RelevantFiles                     []string
	ValidationCommands                      [][]string
	FixCompleted                            bool
	Summary                                 string
}
type Adapter interface {
	Run(context.Context, Request) (Outcome, error)
}
