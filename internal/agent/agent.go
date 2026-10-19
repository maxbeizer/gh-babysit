package agent

import (
	"context"

	"github.com/maxbeizer/gh-babysit/internal/domain"
)

const SchemaVersion = 1

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
	SchemaVersion  int                `json:"schema_version"`
	Workspace      string             `json:"workspace"`
	PullRequest    domain.PullRequest `json:"pull_request"`
	RecordedSHA    string             `json:"recorded_sha"`
	FailedCheck    domain.Check       `json:"failed_check"`
	Mode           Mode               `json:"mode"`
	PriorDiagnosis string             `json:"prior_diagnosis,omitempty"`
	CurrentDiff    string             `json:"current_diff,omitempty"`
	LogPaths       []string           `json:"log_paths"`
	Constraints    []string           `json:"constraints"`
}

type Outcome struct {
	SchemaVersion            int            `json:"schema_version"`
	Classification           Classification `json:"classification"`
	ClassificationConfidence Confidence     `json:"classification_confidence"`
	FixConfidence            Confidence     `json:"fix_confidence"`
	Diagnosis                string         `json:"diagnosis"`
	Plan                     []string       `json:"plan"`
	RelevantFiles            []string       `json:"relevant_files"`
	ValidationCommands       [][]string     `json:"validation_commands"`
	FixCompleted             bool           `json:"fix_completed"`
	Summary                  string         `json:"summary"`
}

type Adapter interface {
	Run(context.Context, Request) (Outcome, error)
}
