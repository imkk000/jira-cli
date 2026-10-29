package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/imkk000/jira-cli/internal/jira"
)

func runTransitions(ctx context.Context, client *jira.Client, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("transitions", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	positional, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: jira transitions [-json] KEY|URL")
	}
	key, err := issueKey(positional[0])
	if err != nil {
		return err
	}
	issue, err := client.Issue(ctx, key)
	if err != nil {
		return err
	}
	transitions, err := client.Transitions(ctx, issue.Key)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(out, transitions)
	}
	return printTransitions(out, issue, transitions)
}

func runMove(ctx context.Context, client *jira.Client, cfg config, args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("move", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	dryRun := fs.Bool("dry-run", false, "show the move without changing the issue")
	anySprint := fs.Bool("any-sprint", false, "allow issues outside $JIRA_SPRINT")
	positional, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 2 {
		return errors.New("usage: jira move [-yes] [-dry-run] [-any-sprint] KEY|URL STATUS")
	}
	key, err := issueKey(positional[0])
	if err != nil {
		return err
	}
	target := strings.Join(positional[1:], " ")

	issue, err := client.Issue(ctx, key)
	if err != nil {
		return err
	}
	if cfg.sprint != "" && !*anySprint {
		if err := requireInSprint(ctx, client, cfg, issue.Key); err != nil {
			return err
		}
	}

	p := &printer{w: out}
	current := issue.Fields.Status.String()
	if strings.EqualFold(current, target) {
		p.printf("%s is already in %s.\n", issue.Key, current)
		return p.err
	}
	transitions, err := client.Transitions(ctx, issue.Key)
	if err != nil {
		return err
	}
	t, err := findTransition(transitions, target)
	if err != nil {
		return fmt.Errorf("%s (%s): %w", issue.Key, current, err)
	}

	p.printf("%s  %s\n  %s -> %s (%s)\n", issue.Key, issue.Fields.Summary, current, t.To.Name, t.Name)
	if *dryRun || p.err != nil {
		return p.err
	}
	ask := promptFrom(in)
	if *yes {
		ask = assumeYes
	}
	return applyMove(ctx, client, issue.Key, t, ask, p)
}

type confirmer func(*printer) (bool, error)

func promptFrom(in io.Reader) confirmer {
	return func(p *printer) (bool, error) { return confirm(in, p) }
}

func assumeYes(*printer) (bool, error) {
	return true, nil
}

func applyMove(ctx context.Context, client *jira.Client, key string, t jira.Transition, ask confirmer, p *printer) error {
	ok, err := ask(p)
	if err != nil {
		return err
	}
	if !ok {
		p.printf("Cancelled.\n")
		return p.err
	}
	if err := client.TransitionIssue(ctx, key, t.ID); err != nil {
		return withScopeHint(err)
	}
	p.printf("Moved %s to %s.\n", key, t.To.Name)
	return p.err
}

func requireInSprint(ctx context.Context, client *jira.Client, cfg config, key string) error {
	if cfg.boardID <= 0 {
		return errMissingBoard
	}
	sprints, err := selectSprints(ctx, client, cfg.boardID, cfg.sprint)
	if err != nil {
		return err
	}
	s := sprints[0]
	issues, err := client.SearchIssues(ctx, fmt.Sprintf("key = %s AND sprint = %d", key, s.ID), 1)
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		return fmt.Errorf("%s is not in sprint %q, pass -any-sprint to move it anyway", key, s.Name)
	}
	return nil
}

func findTransition(transitions []jira.Transition, target string) (jira.Transition, error) {
	target = strings.TrimSpace(target)
	matchers := []func(jira.Transition) bool{
		func(t jira.Transition) bool { return strings.EqualFold(t.To.Name, target) },
		func(t jira.Transition) bool { return t.ID == target || strings.EqualFold(t.Name, target) },
	}
	for _, match := range matchers {
		var matches []jira.Transition
		for _, t := range transitions {
			if match(t) {
				matches = append(matches, t)
			}
		}
		switch len(matches) {
		case 0:
			continue
		case 1:
			return matches[0], nil
		default:
			return jira.Transition{}, fmt.Errorf("%q matches several moves (%s), use the transition ID", target, describeTransitions(matches))
		}
	}
	if len(transitions) == 0 {
		return jira.Transition{}, fmt.Errorf("no moves are available to %q", target)
	}
	return jira.Transition{}, fmt.Errorf("cannot move straight to %q, available: %s", target, describeTransitions(transitions))
}

func describeTransitions(transitions []jira.Transition) string {
	parts := make([]string, 0, len(transitions))
	for _, t := range transitions {
		parts = append(parts, fmt.Sprintf("%q via %q [%s]", t.To.Name, t.Name, t.ID))
	}
	return strings.Join(parts, ", ")
}

func confirm(in io.Reader, p *printer) (bool, error) {
	p.printf("Proceed? [y/N] ")
	if p.err != nil {
		return false, p.err
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func withScopeHint(err error) error {
	var apiErr *jira.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w (the API token probably lacks the write:jira-work scope)", err)
	}
	return err
}

func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
