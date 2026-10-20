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
	ansiPattern      = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)
	timestampPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z\s*`)
	urlPattern       = regexp.MustCompile(`https?://[^\s<>()]+`)
	guidancePattern  = regexp.MustCompile(`(?i)\b(?:run|try|use|next step|recommended|recommendation)\b`)
	signalPattern    = regexp.MustCompile(`(?i)\b(?:error|fail(?:ed|ure)?|fatal|panic|exception|timed out)\b`)
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
		name := strings.TrimSpace(strings.TrimSuffix(line, urls[len(urls)-1]))
		name = strings.Trim(name, " -*:[]()")
		name = signalPattern.ReplaceAllString(name, "")
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
	return selectLines(lines, 3, func(line string) bool {
		return guidancePattern.MatchString(line) || strings.Contains(line, "bin/ci-summary")
	})
}

func signalLines(lines []string) []string {
	return selectLines(lines, 8, func(line string) bool {
		return signalPattern.MatchString(line) && len(urlPattern.FindAllString(line, -1)) == 0
	})
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
		result = append(result, line)
	}
	return result
}
