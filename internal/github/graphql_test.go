package github

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maxbeizer/gh-babysit/internal/process"
)

func TestGraphQLRequirementsUsesPRScopedRequirednessForExactSHA(t *testing.T) {
	var got process.Request
	runner := runnerFunc(func(_ context.Context, request process.Request) (process.Result, error) {
		got = request
		return process.Result{Stdout: []byte(`{"data":{"repository":{"pullRequest":{"headRefOid":"abc","mergeStateStatus":"CLEAN"},"object":{"oid":"abc","statusCheckRollup":{"contexts":{"nodes":[{"__typename":"CheckRun","name":"build","isRequired":true,"checkSuite":{"app":{"databaseId":15368}}},{"__typename":"CheckRun","name":"lint","isRequired":false,"checkSuite":{"app":{"databaseId":15368}}},{"__typename":"StatusContext","context":"legacy","isRequired":true}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}}}`)}, nil
	})
	backend := GraphQLRequirements{Runner: runner, Host: "github.example.com"}
	gotRequirements, err := backend.Requirements(context.Background(), Request{Owner: "acme", Repository: "widgets", SHA: "abc", PullRequestNumber: 7})
	if err != nil {
		t.Fatal(err)
	}
	if !gotRequirements.CompletionAuthoritative || len(gotRequirements.Requirements) != 2 || gotRequirements.Requirements[0].Context != "build" || gotRequirements.Requirements[0].AppID == nil || *gotRequirements.Requirements[0].AppID != 15368 || gotRequirements.Requirements[1].Context != "legacy" || gotRequirements.Requirements[1].AppID != nil {
		t.Fatalf("requirements=%+v", gotRequirements)
	}
	joined := strings.Join(got.Args, " ")
	if got.Name != "gh" || !strings.Contains(joined, "api graphql") || !strings.Contains(joined, "owner=acme") || !strings.Contains(joined, "number=7") || !strings.Contains(joined, "sha=abc") || !strings.Contains(joined, "--hostname github.example.com") {
		t.Fatalf("request=%+v", got)
	}
}

func TestGraphQLRequirementsRequiresCleanMergeStateForCompletion(t *testing.T) {
	runner := runnerFunc(func(context.Context, process.Request) (process.Result, error) {
		return process.Result{Stdout: []byte(`{"data":{"repository":{"pullRequest":{"headRefOid":"abc","mergeStateStatus":"BLOCKED"},"object":{"oid":"abc","statusCheckRollup":{"contexts":{"nodes":[{"__typename":"CheckRun","name":"build","isRequired":true}],"pageInfo":{"hasNextPage":false}}}}}}}`)}, nil
	})
	result, err := (GraphQLRequirements{Runner: runner}).Requirements(context.Background(), Request{Owner: "o", Repository: "r", SHA: "abc", PullRequestNumber: 7})
	if err != nil || result.CompletionAuthoritative || len(result.Requirements) != 1 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestGraphQLRequirementsRejectsDifferentResolvedSHA(t *testing.T) {
	runner := runnerFunc(func(context.Context, process.Request) (process.Result, error) {
		return process.Result{Stdout: []byte(`{"data":{"repository":{"pullRequest":{"headRefOid":"abc","mergeStateStatus":"BLOCKED"},"object":{"oid":"different","statusCheckRollup":{"contexts":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}}`)}, nil
	})
	_, err := (GraphQLRequirements{Runner: runner}).Requirements(context.Background(), Request{Owner: "o", Repository: "r", SHA: "abc", PullRequestNumber: 7})
	if err == nil || !strings.Contains(err.Error(), "different SHA") {
		t.Fatalf("error=%v", err)
	}
}

func TestGraphQLRequirementsPreservesCommandFailure(t *testing.T) {
	runner := runnerFunc(func(context.Context, process.Request) (process.Result, error) {
		return process.Result{Stderr: []byte("gh: Bad credentials (HTTP 401)")}, errors.New("exit 1")
	})
	_, err := (GraphQLRequirements{Runner: runner}).Requirements(context.Background(), Request{Owner: "o", Repository: "r", SHA: "abc", PullRequestNumber: 7})
	if !IsAuthenticationError(err) {
		t.Fatalf("error=%v", err)
	}
}
