package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/imkk000/jira-cli/internal/jira"
)

const (
	dateLayout     = "2006-01-02"
	dateTimeLayout = "2006-01-02 15:04"
	summaryWidth   = 80
)

type printer struct {
	w   io.Writer
	err error
}

func (p *printer) printf(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, args...)
}

func (p *printer) table(write func(*printer)) {
	if p.err != nil {
		return
	}
	tw := tabwriter.NewWriter(p.w, 0, 0, 2, ' ', 0)
	inner := &printer{w: tw}
	write(inner)
	p.err = inner.err
	if p.err == nil {
		p.err = tw.Flush()
	}
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printBoards(out io.Writer, boards []jira.Board) error {
	p := &printer{w: out}
	p.table(func(t *printer) {
		t.printf("ID\tTYPE\tPROJECT\tNAME\n")
		for _, b := range boards {
			t.printf("%d\t%s\t%s\t%s\n", b.ID, b.Type, orDash(b.Location.ProjectKey), b.Name)
		}
	})
	return p.err
}

func printSprintList(out io.Writer, sprints []jira.Sprint, defaultSprint string) error {
	p := &printer{w: out}
	p.table(func(t *printer) {
		t.printf("ID\tSTATE\tSTART\tEND\tNAME\n")
		for _, s := range sprints {
			name := s.Name
			if defaultSprint != "" && matchesSprint(s, defaultSprint) {
				name += " (default)"
			}
			t.printf("%d\t%s\t%s\t%s\t%s\n", s.ID, s.State, formatTime(s.StartDate, dateLayout), formatTime(s.EndDate, dateLayout), name)
		}
	})
	return p.err
}

type issueColumn struct {
	header string
	value  func(jira.Issue) string
}

var (
	keyColumn      = issueColumn{"KEY", func(i jira.Issue) string { return i.Key }}
	typeColumn     = issueColumn{"TYPE", func(i jira.Issue) string { return i.Fields.IssueType.String() }}
	statusColumn   = issueColumn{"STATUS", func(i jira.Issue) string { return i.Fields.Status.String() }}
	priorityColumn = issueColumn{"PRIORITY", func(i jira.Issue) string { return i.Fields.Priority.String() }}
	assigneeColumn = issueColumn{"ASSIGNEE", func(i jira.Issue) string { return i.Fields.Assignee.String() }}
	updatedColumn  = issueColumn{"UPDATED", func(i jira.Issue) string { return formatTime(i.Fields.Updated.Time, dateLayout) }}
	summaryColumn  = issueColumn{"SUMMARY", func(i jira.Issue) string { return truncate(i.Fields.Summary, summaryWidth) }}
)

func urlColumn(browse func(string) string) issueColumn {
	return issueColumn{"URL", func(i jira.Issue) string { return browse(i.Key) }}
}

func defaultColumns(browse func(string) string) []issueColumn {
	return []issueColumn{keyColumn, statusColumn, updatedColumn, urlColumn(browse), summaryColumn}
}

func wideColumns(browse func(string) string) []issueColumn {
	return []issueColumn{keyColumn, typeColumn, statusColumn, priorityColumn, assigneeColumn, updatedColumn, urlColumn(browse), summaryColumn}
}

func printIssues(out io.Writer, issues []jira.Issue, columns []issueColumn) error {
	p := &printer{w: out}
	writeIssues(p, issues, columns)
	return p.err
}

func writeIssues(p *printer, issues []jira.Issue, columns []issueColumn) {
	if len(issues) == 0 {
		p.printf("No issues found.\n")
		return
	}
	p.table(func(t *printer) {
		cells := make([]string, len(columns))
		for i, c := range columns {
			cells[i] = c.header
		}
		t.printf("%s\n", strings.Join(cells, "\t"))
		for _, issue := range issues {
			for i, c := range columns {
				cells[i] = c.value(issue)
			}
			t.printf("%s\n", strings.Join(cells, "\t"))
		}
	})
}

func printSprints(out io.Writer, results []sprintIssues, columns []issueColumn) error {
	p := &printer{w: out}
	for i, r := range results {
		if i > 0 {
			p.printf("\n")
		}
		s := r.Sprint
		if s.StartDate.IsZero() && s.EndDate.IsZero() {
			p.printf("%s [%s]\n", s.Name, s.State)
		} else {
			p.printf("%s [%s] (%s -> %s)\n", s.Name, s.State, formatTime(s.StartDate, dateLayout), formatTime(s.EndDate, dateLayout))
		}
		if s.Goal != "" {
			p.printf("Goal: %s\n", s.Goal)
		}
		p.printf("\n")
		writeIssues(p, r.Issues, columns)
	}
	return p.err
}

func printIssue(out io.Writer, issue jira.Issue, browseURL string) error {
	f := issue.Fields
	p := &printer{w: out}
	p.printf("%s  %s\n\n", issue.Key, f.Summary)
	p.table(func(t *printer) {
		t.printf("Type:\t%s\n", f.IssueType)
		t.printf("Status:\t%s\n", f.Status)
		t.printf("Priority:\t%s\n", f.Priority)
		t.printf("Assignee:\t%s\n", f.Assignee)
		t.printf("Reporter:\t%s\n", f.Reporter)
		t.printf("Labels:\t%s\n", orDash(strings.Join(f.Labels, ", ")))
		t.printf("Created:\t%s\n", formatTime(f.Created.Time, dateTimeLayout))
		t.printf("Updated:\t%s\n", formatTime(f.Updated.Time, dateTimeLayout))
		t.printf("URL:\t%s\n", browseURL)
	})
	if f.Description != "" {
		p.printf("\n%s\n", f.Description)
	}
	if len(issue.Comments) > 0 {
		p.printf("\nComments (%d)\n", len(issue.Comments))
		for _, c := range issue.Comments {
			p.printf("\n%s  %s\n%s\n", c.Author, formatTime(c.Created.Time, dateTimeLayout), c.Body)
		}
	}
	return p.err
}

func printTransitions(out io.Writer, issue jira.Issue, transitions []jira.Transition) error {
	p := &printer{w: out}
	p.printf("%s  %s\nStatus: %s\n\n", issue.Key, issue.Fields.Summary, issue.Fields.Status)
	if len(transitions) == 0 {
		p.printf("No moves available.\n")
		return p.err
	}
	p.table(func(t *printer) {
		t.printf("TO STATUS\tACTION\tID\n")
		for _, tr := range transitions {
			t.printf("%s\t%s\t%s\n", tr.To.Name, tr.Name, tr.ID)
		}
	})
	return p.err
}

func formatTime(t time.Time, layout string) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format(layout)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-3]) + "..."
}
