package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/imkk000/jira-cli/internal/jira"
)

type issueFilter struct {
	boardID  int
	status   string
	assignee string
	jql      string
	limit    int
	open     bool
	wide     bool
	json     bool
}

func newIssueFlags(name string, defaultBoard int) (*flag.FlagSet, *issueFilter) {
	f := &issueFilter{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.IntVar(&f.boardID, "board", defaultBoard, "board ID (defaults to $JIRA_BOARD_ID)")
	fs.StringVar(&f.status, "status", "", `only issues in this status, e.g. "In Progress"`)
	fs.StringVar(&f.assignee, "assignee", "", `only issues assigned to "me", "none", or a user ID/email`)
	fs.StringVar(&f.jql, "jql", "", "extra JQL filter clause")
	fs.IntVar(&f.limit, "limit", 100, "maximum issues to fetch, 0 for all")
	fs.BoolVar(&f.open, "open", false, "hide issues whose status is in the Done category")
	fs.BoolVar(&f.wide, "wide", false, "also show TYPE, PRIORITY and ASSIGNEE")
	fs.BoolVar(&f.json, "json", false, "print JSON")
	return fs, f
}

func (f *issueFilter) columns(browse func(string) string) []issueColumn {
	if f.wide {
		return wideColumns(browse)
	}
	return defaultColumns(browse)
}

var errMissingBoard = errors.New(`board ID required: pass -board or set JIRA_BOARD_ID (find it with "jira boards")`)

func (f *issueFilter) validate() error {
	if f.boardID <= 0 {
		return errMissingBoard
	}
	if f.limit < 0 {
		return errors.New("-limit must be >= 0")
	}
	return nil
}

func (f *issueFilter) query() string {
	var clauses []string
	if f.status != "" {
		clauses = append(clauses, "status = "+quoteJQL(f.status))
	}
	switch f.assignee {
	case "":
	case "me":
		clauses = append(clauses, "assignee = currentUser()")
	case "none":
		clauses = append(clauses, "assignee is EMPTY")
	default:
		clauses = append(clauses, "assignee = "+quoteJQL(f.assignee))
	}
	if f.open {
		clauses = append(clauses, "statusCategory != Done")
	}
	if f.jql != "" {
		clauses = append(clauses, "("+f.jql+")")
	}
	return strings.Join(clauses, " AND ")
}

func quoteJQL(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func runBoards(ctx context.Context, client *jira.Client, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("boards", flag.ContinueOnError)
	name := fs.String("name", "", "only boards whose name contains TEXT")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	boards, err := client.Boards(ctx, *name)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, boards)
	}
	return printBoards(out, boards)
}

func runIssues(ctx context.Context, client *jira.Client, cfg config, args []string, out io.Writer) error {
	fs, f := newIssueFlags("issues", cfg.boardID)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := f.validate(); err != nil {
		return err
	}
	issues, err := client.BoardIssues(ctx, f.boardID, f.query(), f.limit)
	if err != nil {
		return err
	}
	if f.json {
		return writeJSON(out, issues)
	}
	browse, err := client.BrowseURLs(ctx)
	if err != nil {
		return err
	}
	return printIssues(out, issues, f.columns(browse))
}

type sprintIssues struct {
	Sprint jira.Sprint  `json:"sprint"`
	Issues []jira.Issue `json:"issues"`
}

func runSprint(ctx context.Context, client *jira.Client, cfg config, args []string, out io.Writer) error {
	fs, f := newIssueFlags("sprint", cfg.boardID)
	ref := fs.String("sprint", cfg.sprint, "sprint name or ID (defaults to $JIRA_SPRINT, else the active sprint)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := f.validate(); err != nil {
		return err
	}
	sprints, err := selectSprints(ctx, client, f.boardID, *ref)
	if err != nil {
		return err
	}
	results := make([]sprintIssues, 0, len(sprints))
	for _, s := range sprints {
		issues, err := client.SearchIssues(ctx, sprintJQL(s.ID, f.query()), f.limit)
		if err != nil {
			return fmt.Errorf("sprint %q: %w", s.Name, err)
		}
		results = append(results, sprintIssues{Sprint: s, Issues: issues})
	}
	if f.json {
		return writeJSON(out, results)
	}
	browse, err := client.BrowseURLs(ctx)
	if err != nil {
		return err
	}
	return printSprints(out, results, f.columns(browse))
}

func selectSprints(ctx context.Context, client *jira.Client, boardID int, ref string) ([]jira.Sprint, error) {
	if ref == "" {
		active, err := client.Sprints(ctx, boardID, "active")
		if err != nil {
			return nil, err
		}
		if len(active) == 0 {
			return nil, fmt.Errorf("board %d has no active sprint, pass -sprint or set JIRA_SPRINT", boardID)
		}
		return active, nil
	}
	for _, states := range [][]string{{"active", "future"}, {"closed"}} {
		sprints, err := client.Sprints(ctx, boardID, states...)
		if err != nil {
			return nil, err
		}
		if i := slices.IndexFunc(sprints, func(s jira.Sprint) bool { return matchesSprint(s, ref) }); i >= 0 {
			return sprints[i : i+1], nil
		}
	}
	return nil, fmt.Errorf(`no sprint %q on board %d, list them with "jira sprints -all"`, ref, boardID)
}

func matchesSprint(s jira.Sprint, ref string) bool {
	return strconv.Itoa(s.ID) == ref || strings.EqualFold(s.Name, strings.TrimSpace(ref))
}

func sprintJQL(sprintID int, filter string) string {
	jql := fmt.Sprintf("sprint = %d", sprintID)
	if filter != "" {
		jql += " AND " + filter
	}
	return jql + " ORDER BY Rank ASC"
}

func runSprints(ctx context.Context, client *jira.Client, cfg config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sprints", flag.ContinueOnError)
	boardID := fs.Int("board", cfg.boardID, "board ID (defaults to $JIRA_BOARD_ID)")
	all := fs.Bool("all", false, "include closed sprints")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *boardID <= 0 {
		return errMissingBoard
	}
	states := []string{"active", "future"}
	if *all {
		states = append(states, "closed")
	}
	sprints, err := client.Sprints(ctx, *boardID, states...)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, sprints)
	}
	return printSprintList(out, sprints, cfg.sprint)
}

func runShow(ctx context.Context, client *jira.Client, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	positional, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: jira show [-json] KEY")
	}
	issue, err := client.Issue(ctx, positional[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, issue)
	}
	browse, err := client.BrowseURLs(ctx)
	if err != nil {
		return err
	}
	return printIssue(out, issue, browse(issue.Key))
}
