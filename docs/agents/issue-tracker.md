# Issue tracker: GitHub

Issues and specifications for this repository live in GitHub Issues at `maxbeizer/gh-babysit`. Use the authenticated `gh` CLI for operations so private-resource access and repository identity are preserved.

## Conventions

- Create: `gh issue create --repo maxbeizer/gh-babysit --title "..." --body "..."`.
- Read: `gh issue view <number> --repo maxbeizer/gh-babysit --json number,title,body,labels,comments`.
- List: `gh issue list --repo maxbeizer/gh-babysit --state open` with appropriate label filters.
- Comment: `gh issue comment <number> --repo maxbeizer/gh-babysit --body "..."`.
- Label: `gh issue edit <number> --repo maxbeizer/gh-babysit --add-label "..."`.
- Close: `gh issue close <number> --repo maxbeizer/gh-babysit --comment "..."`.
- Use GitHub Sub-Issues for parent/child work and native issue dependencies for blocking edges when available.

When a skill says to publish to the issue tracker, create a GitHub issue. When it says to fetch a ticket, read the corresponding issue and its relevant comments.

## Pull requests as a triage surface

**PRs as a request surface: no.** Pull requests are not included in the triage queue unless this setting is deliberately changed later.

GitHub shares one number space across issues and pull requests. Resolve ambiguous references explicitly rather than assuming their type.
