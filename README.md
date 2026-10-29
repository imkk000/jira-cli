# jira-cli

CLI for listing, viewing, and moving issues on a Jira board. It uses only the Go standard library.

## Setup

1. Create an API token at https://id.atlassian.com/manage-profile/security/api-tokens (Jira Cloud). On Server or Data Center, create a personal access token and leave `JIRA_EMAIL` unset.
2. Put the configuration in `.env` in the directory you run `jira` from (real environment variables override it):

```sh
JIRA_URL=https://your-domain.atlassian.net
JIRA_EMAIL=you@example.com
JIRA_API_TOKEN=your-token
JIRA_BOARD_ID=42
JIRA_SPRINT="Support"
```

The board ID appears in the board URL (`.../boards/42`), or you can look it up with `jira boards`. `JIRA_SPRINT` is optional and accepts a sprint name or ID; without it, `jira sprint` shows the active sprint.

Scoped API tokens only work through `https://api.atlassian.com/ex/jira/<cloudId>`; get the cloud ID from `https://your-domain.atlassian.net/_edge/tenant_info`.

3. Build: `task build` (outputs `bin/jira`).

## Usage

```sh
jira boards -name team
jira sprints -all
jira sprint -open
jira sprint -sprint "Sprint 1" -assignee me
jira sprint -jql 'created < "2026-10-21"'
jira issues -status "In Progress"
jira show PROJ-123
jira transitions PROJ-123
jira move -dry-run PROJ-123 In Code Review
jira move PROJ-123 "In Code Review"
```

| Command       | Description                                                          |
| ------------- | -------------------------------------------------------------------- |
| `boards`      | List boards visible to you                                           |
| `sprints`     | List active and future sprints (`-all` adds closed)                  |
| `sprint`      | List issues in `-sprint`, else `JIRA_SPRINT`, else the active sprint |
| `issues`      | List issues on the board                                             |
| `show`        | Show one issue with its description                                  |
| `transitions` | List the status moves available for an issue right now               |
| `move`        | Move an issue to a status reachable in one step                      |

`issues` and `sprint` accept `-status`, `-assignee me|none|USER`, `-open` (hide Done-category statuses), `-jql`, and `-limit`. `show`, `transitions`, and every list command accept `-json`.

`move` matches the target by status name, action name, or transition ID, prints the planned move, and asks for confirmation (`-yes` skips it, `-dry-run` stops before changing anything). When `JIRA_SPRINT` is set, it refuses issues outside that sprint unless `-any-sprint` is given. Jira workflows only allow one step at a time, so moving across several statuses takes several `move` calls. Changing status needs the `write:jira-work` scope on scoped tokens.

## Development

```sh
task check
```
