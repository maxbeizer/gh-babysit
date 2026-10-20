package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/maxbeizer/gh-babysit/internal/clock"
	"github.com/maxbeizer/gh-babysit/internal/exitcode"
	gh "github.com/maxbeizer/gh-babysit/internal/github"
	"github.com/maxbeizer/gh-babysit/internal/preflight"
	"github.com/maxbeizer/gh-babysit/internal/process"
	"github.com/maxbeizer/gh-babysit/internal/report"
	"github.com/maxbeizer/gh-babysit/internal/watcher"
	"github.com/spf13/cobra"
)

type PullRequestArgument struct {
	Number int
	URL    string
}

func ParsePullRequestArgument(value string) (PullRequestArgument, error) {
	if number, err := strconv.Atoi(value); err == nil {
		if number <= 0 {
			return PullRequestArgument{}, fmt.Errorf("pull request number must be positive")
		}
		return PullRequestArgument{Number: number}, nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || !strings.Contains(parsed.Path, "/pull/") {
		return PullRequestArgument{}, fmt.Errorf("pull request must be a positive number or pull request URL")
	}
	return PullRequestArgument{URL: value}, nil
}

type Options struct {
	PullRequest                              string
	Auto                                     bool
	ShowLogs                                 bool
	AgentCommand                             string
	PollInterval, AppearanceTimeout, Timeout time.Duration
}

func NewCommand(version string) *cobra.Command {
	return NewCommandWithRunner(version, process.ExecRunner{})
}

func qualifiedRepository(host, repository string) string {
	if host == "" {
		return repository
	}
	return host + "/" + repository
}

func NewCommandWithRunner(version string, runner process.Runner) *cobra.Command {
	o := Options{}
	cmd := &cobra.Command{
		Use:           "gh-babysit [<pr-number-or-url>]",
		Short:         "Watch required pull request checks and safely delegate fixes",
		Version:       version,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if _, err := ParsePullRequestArgument(args[0]); err != nil {
					return err
				}
				o.PullRequest = args[0]
			}
			result, err := (preflight.Checker{Runner: runner}).Run(cmd.Context(), o.PullRequest)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Watching required checks for PR #%d (%s) at %.12s.\n", result.PullRequest.Number, result.PullRequest.URL, result.Baseline.HeadSHA)
			source, err := gh.NewWatchSource(runner, result.Root, result.PullRequest, result.Baseline.HeadSHA, result.Baseline.Status, result.Baseline.Index)
			if err != nil {
				return exitcode.New(exitcode.Usage, err)
			}
			watchResult, err := watcher.Run(cmd.Context(), source, clock.Real{}, watcher.Options{RecordedSHA: result.Baseline.HeadSHA, Repository: qualifiedRepository(result.PullRequest.Host, result.PullRequest.Repository), PollInterval: o.PollInterval, AppearanceTimeout: o.AppearanceTimeout, Timeout: o.Timeout, Output: cmd.OutOrStdout()})
			if err != nil {
				if cause := context.Cause(cmd.Context()); cause != nil {
					return cause
				}
				if errors.Is(err, watcher.ErrTimeout) {
					return exitcode.New(exitcode.Timeout, err)
				}
				if gh.IsAuthenticationError(err) || errors.Is(err, exec.ErrNotFound) {
					return exitcode.New(exitcode.Usage, err)
				}
				return exitcode.New(exitcode.SafetyStop, err)
			}
			if watchResult.State == watcher.Failed {
				report.Failure(cmd.OutOrStdout(), report.FailureOptions{Check: watchResult.Failed, Repository: qualifiedRepository(result.PullRequest.Host, result.PullRequest.Repository), Logs: watchResult.Logs, ShowLogs: o.ShowLogs})
				if watchResult.RerunSuggestion != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "\nSafe suggestion (not executed):\n  %s\n", watchResult.RerunSuggestion)
				}
				return exitcode.NewReported(exitcode.NotFixable, fmt.Errorf("required check %s failed", watchResult.Failed.Context))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&o.Auto, "auto", false, "permit confident fixes, validation, commits, and pushes")
	cmd.Flags().BoolVar(&o.ShowLogs, "show-logs", false, "print the bounded raw failed-check log")
	cmd.Flags().StringVar(&o.AgentCommand, "agent-command", "copilot", "agent executable")
	cmd.Flags().DurationVar(&o.PollInterval, "poll-interval", 15*time.Second, "required-check polling interval")
	cmd.Flags().DurationVar(&o.AppearanceTimeout, "appearance-timeout", 10*time.Minute, "required-suite appearance timeout")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", 6*time.Hour, "overall watch timeout")
	return cmd
}
