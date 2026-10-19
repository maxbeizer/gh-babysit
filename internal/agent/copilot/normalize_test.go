package copilot

import (
	"strings"
	"testing"

	"github.com/maxbeizer/gh-babysit/internal/agent"
)

const validOutcome = `{"schema_version":1,"classification":"code_fixable","classification_confidence":"high","fix_confidence":"medium","diagnosis":"broken test","plan":["edit test"],"relevant_files":["x_test.go"],"validation_commands":[["go","test","./..."]],"fix_completed":false,"summary":"a test is stale"}`

func TestNormalize(t *testing.T) {
	outcome, err := Normalize([]byte(validOutcome))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.ClassificationConfidence != agent.High || outcome.FixConfidence != agent.Medium {
		t.Fatalf("confidence fields were not kept separate: %+v", outcome)
	}
	if got := outcome.ValidationCommands[0]; len(got) != 3 || got[0] != "go" {
		t.Fatalf("unexpected command: %#v", got)
	}
}

func TestNormalizeRejectsInvalidResponses(t *testing.T) {
	tests := map[string]string{
		"prose":          "Here is the result: " + validOutcome,
		"trailing value": validOutcome + `{}`,
		"unknown field":  strings.Replace(validOutcome, `"summary":`, `"extra":true,"summary":`, 1),
		"missing field":  strings.Replace(validOutcome, `,"summary":"a test is stale"`, "", 1),
		"bad enum":       strings.Replace(validOutcome, "code_fixable", "certainly_broken", 1),
		"empty command":  strings.Replace(validOutcome, `[["go","test","./..."]]`, `[[]]`, 1),
		"wrong version":  strings.Replace(validOutcome, `"schema_version":1`, `"schema_version":2`, 1),
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Normalize([]byte(response)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestBuildPromptIncludesContractAndRequest(t *testing.T) {
	prompt, err := BuildPrompt(agent.Request{Mode: agent.Diagnose, Workspace: "/snapshot", Constraints: []string{"do not edit"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"schema_version": 1`, `"classification_confidence"`, `"fix_confidence"`, `"mode": "diagnose"`, "exactly one JSON object"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt does not contain %q", want)
		}
	}
}
