package copilot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/maxbeizer/gh-babysit/internal/agent"
)

// Normalize validates the complete Copilot response as one v1 outcome object.
func Normalize(response []byte) (agent.Outcome, error) {
	if len(bytes.TrimSpace(response)) == 0 {
		return agent.Outcome{}, errors.New("copilot returned an empty response")
	}
	var raw struct {
		SchemaVersion            *int                  `json:"schema_version"`
		Classification           *agent.Classification `json:"classification"`
		ClassificationConfidence *agent.Confidence     `json:"classification_confidence"`
		FixConfidence            *agent.Confidence     `json:"fix_confidence"`
		Diagnosis                *string               `json:"diagnosis"`
		Plan                     *[]*string            `json:"plan"`
		RelevantFiles            *[]*string            `json:"relevant_files"`
		ValidationCommands       *[][]string           `json:"validation_commands"`
		FixCompleted             *bool                 `json:"fix_completed"`
		Summary                  *string               `json:"summary"`
	}
	dec := json.NewDecoder(bytes.NewReader(response))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return agent.Outcome{}, fmt.Errorf("decode copilot outcome: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return agent.Outcome{}, errors.New("copilot response contains more than one JSON value")
		}
		return agent.Outcome{}, fmt.Errorf("copilot response has trailing content: %w", err)
	}
	if raw.SchemaVersion == nil || raw.Classification == nil || raw.ClassificationConfidence == nil || raw.FixConfidence == nil || raw.Diagnosis == nil || raw.Plan == nil || raw.RelevantFiles == nil || raw.ValidationCommands == nil || raw.FixCompleted == nil || raw.Summary == nil {
		return agent.Outcome{}, errors.New("copilot outcome is missing a required field")
	}
	if *raw.SchemaVersion != agent.SchemaVersion {
		return agent.Outcome{}, fmt.Errorf("unsupported outcome schema version %d", *raw.SchemaVersion)
	}
	if !validClassification(*raw.Classification) {
		return agent.Outcome{}, fmt.Errorf("unknown classification %q", *raw.Classification)
	}
	if !validConfidence(*raw.ClassificationConfidence) || !validConfidence(*raw.FixConfidence) {
		return agent.Outcome{}, errors.New("confidence must be high, medium, or low")
	}
	if strings.TrimSpace(*raw.Diagnosis) == "" || strings.TrimSpace(*raw.Summary) == "" {
		return agent.Outcome{}, errors.New("diagnosis and summary must not be empty")
	}
	plan, err := nonNullStrings("plan", *raw.Plan)
	if err != nil {
		return agent.Outcome{}, err
	}
	relevantFiles, err := nonNullStrings("relevant_files", *raw.RelevantFiles)
	if err != nil {
		return agent.Outcome{}, err
	}
	for _, command := range *raw.ValidationCommands {
		if len(command) == 0 {
			return agent.Outcome{}, errors.New("validation commands must be non-empty argument arrays")
		}
		for _, arg := range command {
			if arg == "" || strings.ContainsRune(arg, '\x00') {
				return agent.Outcome{}, errors.New("validation command arguments must be non-empty and contain no NUL bytes")
			}
		}
	}
	return agent.Outcome{
		SchemaVersion: *raw.SchemaVersion, Classification: *raw.Classification,
		ClassificationConfidence: *raw.ClassificationConfidence, FixConfidence: *raw.FixConfidence,
		Diagnosis: *raw.Diagnosis, Plan: plan, RelevantFiles: relevantFiles,
		ValidationCommands: *raw.ValidationCommands, FixCompleted: *raw.FixCompleted, Summary: *raw.Summary,
	}, nil
}

func nonNullStrings(field string, values []*string) ([]string, error) {
	result := make([]string, len(values))
	for i, value := range values {
		if value == nil {
			return nil, fmt.Errorf("%s must not contain null", field)
		}
		result[i] = *value
	}
	return result, nil
}

func validClassification(v agent.Classification) bool {
	switch v {
	case agent.CodeFixable, agent.FlakyOrInfrastructure, agent.Ambiguous, agent.InsufficientEvidence:
		return true
	default:
		return false
	}
}

func validConfidence(v agent.Confidence) bool {
	return v == agent.High || v == agent.Medium || v == agent.Low
}
