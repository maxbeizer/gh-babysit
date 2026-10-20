package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/maxbeizer/gh-babysit/internal/process"
)

// GraphQLRequirements reads requiredness from the PR's status-check rollup when
// configuration APIs are unavailable to an integration token.
type GraphQLRequirements struct {
	Runner process.Runner
	Host   string
}

const requirednessQuery = `query($owner:String!,$repository:String!,$sha:String!,$number:Int!,$endCursor:String){repository(owner:$owner,name:$repository){pullRequest(number:$number){headRefOid mergeStateStatus} object(expression:$sha){... on Commit{oid statusCheckRollup{contexts(first:100,after:$endCursor){nodes{__typename ... on CheckRun{name isRequired(pullRequestNumber:$number) checkSuite{app{databaseId}}} ... on StatusContext{context isRequired(pullRequestNumber:$number)}} pageInfo{hasNextPage endCursor}}}}}}}`

func (g GraphQLRequirements) Requirements(ctx context.Context, req Request) (PullRequestRequirementResult, error) {
	if g.Runner == nil {
		return PullRequestRequirementResult{}, errors.New("GraphQL runner is nil")
	}
	args := []string{"api", "graphql", "--paginate", "-f", "query=" + requirednessQuery,
		"-F", "owner=" + req.Owner, "-F", "repository=" + req.Repository,
		"-F", "sha=" + req.SHA, "-F", "number=" + strconv.Itoa(req.PullRequestNumber)}
	if g.Host != "" {
		args = append(args, "--hostname", g.Host)
	}
	result, err := g.Runner.Run(ctx, process.Request{Name: "gh", Args: args})
	if err != nil {
		if ctx.Err() != nil {
			return PullRequestRequirementResult{}, ctx.Err()
		}
		return PullRequestRequirementResult{}, watchCommandError("gh api graphql", result, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(result.Stdout))
	var requirements []Requirement
	var mergeState string
	pages := 0
	for {
		var response struct {
			Data struct {
				Repository struct {
					PullRequest *struct {
						HeadRefOID       string `json:"headRefOid"`
						MergeStateStatus string `json:"mergeStateStatus"`
					} `json:"pullRequest"`
					Object *struct {
						OID               string `json:"oid"`
						StatusCheckRollup *struct {
							Contexts struct {
								Nodes []struct {
									Type       string `json:"__typename"`
									Name       string `json:"name"`
									Context    string `json:"context"`
									IsRequired bool   `json:"isRequired"`
									CheckSuite struct {
										App *struct {
											DatabaseID int64 `json:"databaseId"`
										} `json:"app"`
									} `json:"checkSuite"`
								} `json:"nodes"`
							} `json:"contexts"`
						} `json:"statusCheckRollup"`
					} `json:"object"`
				} `json:"repository"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := decoder.Decode(&response); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return PullRequestRequirementResult{}, fmt.Errorf("decode PR-scoped required checks: %w", err)
		}
		pages++
		if len(response.Errors) > 0 {
			return PullRequestRequirementResult{}, fmt.Errorf("GraphQL required-check query: %s", response.Errors[0].Message)
		}
		pr := response.Data.Repository.PullRequest
		if pr == nil || pr.HeadRefOID != req.SHA {
			return PullRequestRequirementResult{}, errors.New("GraphQL required-check query did not resolve the recorded PR head")
		}
		if mergeState != "" && mergeState != pr.MergeStateStatus {
			return PullRequestRequirementResult{}, errors.New("PR merge state changed while reading required checks")
		}
		mergeState = pr.MergeStateStatus
		object := response.Data.Repository.Object
		if object == nil || object.OID == "" {
			return PullRequestRequirementResult{}, errors.New("GraphQL required-check query did not resolve the recorded commit")
		}
		if object.OID != req.SHA {
			return PullRequestRequirementResult{}, fmt.Errorf("GraphQL required-check query resolved different SHA %s", object.OID)
		}
		if object.StatusCheckRollup == nil {
			continue
		}
		for _, node := range object.StatusCheckRollup.Contexts.Nodes {
			if !node.IsRequired {
				continue
			}
			requirement := Requirement{Authorities: []string{"pull_request_rollup"}}
			switch node.Type {
			case "CheckRun":
				requirement.Context = node.Name
				if node.CheckSuite.App != nil {
					appID := node.CheckSuite.App.DatabaseID
					requirement.AppID = &appID
				}
			case "StatusContext":
				requirement.Context = node.Context
			default:
				return PullRequestRequirementResult{}, fmt.Errorf("unsupported status-check rollup type %q", node.Type)
			}
			if requirement.Context == "" {
				return PullRequestRequirementResult{}, errors.New("PR status-check rollup returned an empty context")
			}
			requirements = append(requirements, requirement)
		}
	}
	if pages == 0 {
		return PullRequestRequirementResult{}, errors.New("GraphQL required-check query returned no response")
	}
	// CLEAN is GitHub's positive signal that no branch-protection requirement,
	// including an uncreated required check, blocks this exact PR head.
	return PullRequestRequirementResult{Requirements: mergeRequirements(requirements), CompletionAuthoritative: mergeState == "CLEAN"}, nil
}
