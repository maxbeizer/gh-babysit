// Package report formats concise, actionable terminal reports.
package report

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	gh "github.com/maxbeizer/gh-babysit/internal/github"
)

var (
	ansiPattern         = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)
	caretANSIPattern    = regexp.MustCompile(`\^\[\[[0-?]*[ -/]*[@-~]`)
	timestampPattern    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z\s*`)
	actionsCommand      = regexp.MustCompile(`##\[[^]]*\]`)
	urlPattern          = regexp.MustCompile(`https?://[^\s<>()]+`)
	explicitGuidance    = regexp.MustCompile(`(?i)\b(?:next steps?|recommended|recommendation|you (?:can|should)|please (?:run|use))\b`)
	actionableCommand   = regexp.MustCompile(`(?:^|\s)(?:\./|bin/|scripts?/)[[:alnum:]_./-]+`)
	signalPattern       = regexp.MustCompile(`(?i)\b(?:error|fail(?:ed|ure)?|fatal|panic|exception|timed out)\b`)
	failureAffixPattern = regexp.MustCompile(`(?i)^(?:error\s*:\s*|fail(?:ed|ure)?\s+)|\s+(?:error|fail(?:ed|ure)?)(?:\s+child)?\s*$`)
)

// FailureOptions contains the already bounded failed-check log and enough
// structured identity to direct the user to the complete GitHub Actions log.
type FailureOptions struct {
	Check      gh.Observation
	Repository string
	Logs       string
	ShowLogs   bool
}

// Failure writes one failure report. Raw logs are included only when requested.
func Failure(out io.Writer, options FailureOptions) {
	if out == nil {
		return
	}
	fmt.Fprintf(out, "\nRequired check failed: %s\n%s\n", options.Check.Context, options.Check.URL)

	lines := cleanLines(options.Logs)
	jobs := failedJobs(lines, options.Check.URL)
	if len(jobs) > 0 {
		fmt.Fprintln(out, "\nFailed jobs:")
		for _, job := range jobs {
			fmt.Fprintf(out, "  - %s\n", job.name)
			if job.url != "" {
				fmt.Fprintf(out, "    %s\n", job.url)
			}
		}
	}
	if guidance := guidanceLines(lines); len(guidance) > 0 {
		fmt.Fprintln(out, "\nSuggested next step:")
		for _, line := range guidance {
			fmt.Fprintf(out, "  %s\n", line)
		}
	}
	if signals := signalLines(lines); len(signals) > 0 {
		fmt.Fprintln(out, "\nFailure details:")
		for _, line := range signals {
			fmt.Fprintf(out, "  %s\n", line)
		}
	}

	if options.Check.WorkflowRunID != nil && options.Repository != "" {
		fmt.Fprintf(out, "\nFull failed logs:\n  gh run view %d --log-failed --repo %s\n", *options.Check.WorkflowRunID, options.Repository)
	}
	if options.ShowLogs {
		fmt.Fprintf(out, "\nRaw failed logs:\n%s\n", options.Logs)
	}
}

type failedJob struct{ name, url string }

func failedJobs(lines []string, checkURL string) []failedJob {
	var result []failedJob
	seen := map[string]bool{}
	for _, line := range lines {
		if !signalPattern.MatchString(line) {
			continue
		}
		urls := urlPattern.FindAllString(line, -1)
		url := ""
		if len(urls) > 0 {
			url = strings.TrimRight(urls[len(urls)-1], ".,;:")
		}
		if url == "" || url == checkURL {
			continue
		}
		name := strings.TrimSpace(strings.Replace(line, urls[len(urls)-1], "", 1))
		name = strings.TrimRight(name, " \t()")
		if slash := strings.LastIndex(name, " / "); slash >= 0 {
			name = name[slash+3:]
		}
		name = strings.TrimSpace(failureAffixPattern.ReplaceAllString(name, ""))
		name = strings.Trim(name, " -*:[]()")
		if name == "" || seen[url] {
			continue
		}
		seen[url] = true
		result = append(result, failedJob{name: name, url: url})
		if len(result) == 8 {
			break
		}
	}
	return result
}

func guidanceLines(lines []string) []string {
	const limit = 3
	result := selectLines(lines, limit, func(line string) bool {
		return !implementationLine(line) && actionableCommand.MatchString(line)
	})
	seen := make(map[string]bool, len(result))
	for _, line := range result {
		seen[line] = true
	}
	for _, line := range selectLines(lines, limit, func(line string) bool {
		return !implementationLine(line) && explicitGuidance.MatchString(line)
	}) {
		if len(result) == limit {
			break
		}
		if !seen[line] {
			result = append(result, line)
			seen[line] = true
		}
	}
	return result
}

func signalLines(lines []string) []string {
	const limit = 8
	selected := map[int]bool{}
	for i, line := range lines {
		if !signalPattern.MatchString(line) || urlPattern.MatchString(line) || implementationLine(line) {
			continue
		}
		for j := max(0, i-1); j <= min(len(lines)-1, i+1); j++ {
			if lines[j] != "" && !urlPattern.MatchString(lines[j]) && !explicitGuidance.MatchString(lines[j]) && !implementationLine(lines[j]) {
				selected[j] = true
			}
		}
	}
	result, seen := make([]string, 0, limit), map[string]bool{}
	for i, line := range lines {
		if !selected[i] || seen[line] {
			continue
		}
		seen[line] = true
		result = append(result, line)
		if len(result) == limit {
			break
		}
	}
	return result
}

func implementationLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Contains(trimmed, "failed_jobs") || aggregateStatusLine(trimmed) ||
		strings.HasPrefix(trimmed, "if [[") || strings.HasPrefix(trimmed, "if [") ||
		strings.HasPrefix(trimmed, "for ") || strings.HasPrefix(trimmed, "while ")
}

func aggregateStatusLine(line string) bool {
	structured := strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[")
	hasIdentity := strings.Contains(line, `"name"`) || strings.Contains(line, `"job"`)
	hasStatus := strings.Contains(line, `"conclusion"`) || strings.Contains(line, `"status"`)
	return structured && hasIdentity && hasStatus
}

func selectLines(lines []string, limit int, match func(string) bool) []string {
	result, seen := make([]string, 0, limit), map[string]bool{}
	for _, line := range lines {
		if line == "" || !match(line) || seen[line] {
			continue
		}
		seen[line] = true
		result = append(result, line)
		if len(result) == limit {
			break
		}
	}
	return result
}

func cleanLines(logs string) []string {
	raw := strings.Split(strings.ReplaceAll(logs, "\r\n", "\n"), "\n")
	result := make([]string, 0, len(raw))
	for _, line := range raw {
		line = ansiPattern.ReplaceAllString(line, "")
		line = caretANSIPattern.ReplaceAllString(line, "")
		groupRun := strings.Contains(line, "##[group]Run ")
		errorAnnotation := strings.Contains(line, "##[error]")
		fields := strings.Split(line, "\t")
		for i, field := range fields {
			trimmed := strings.TrimSpace(field)
			if timestampPattern.MatchString(trimmed) {
				line = strings.Join(fields[i:], " ")
				break
			}
		}
		line = timestampPattern.ReplaceAllString(strings.TrimSpace(line), "")
		line = strings.TrimSpace(strings.TrimPrefix(line, "UNKNOWN STEP"))
		line = strings.TrimSpace(actionsCommand.ReplaceAllString(line, ""))
		if groupRun {
			line = ""
		} else if errorAnnotation && !signalPattern.MatchString(line) {
			line = "Error: " + line
		}
		result = append(result, line)
	}
	return result
}
