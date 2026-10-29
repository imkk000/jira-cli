package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imkk000/jira-cli/internal/jira"
)

func TestIssueFilterQuery(t *testing.T) {
	cases := []struct {
		name   string
		filter issueFilter
		want   string
	}{
		{"empty", issueFilter{}, ""},
		{"status", issueFilter{status: "In Progress"}, `status = "In Progress"`},
		{"me", issueFilter{assignee: "me"}, "assignee = currentUser()"},
		{"none", issueFilter{assignee: "none"}, "assignee is EMPTY"},
		{"escaped", issueFilter{assignee: `a"b\c`}, `assignee = "a\"b\\c"`},
		{"open", issueFilter{open: true}, "statusCategory != Done"},
		{
			"combined",
			issueFilter{status: "Done", assignee: "me", jql: "type = Bug OR type = Task"},
			`status = "Done" AND assignee = currentUser() AND (type = Bug OR type = Task)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.filter.query(); got != tc.want {
				t.Errorf("query() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIssueFilterValidate(t *testing.T) {
	if err := (&issueFilter{}).validate(); err == nil {
		t.Error("missing board accepted")
	}
	if err := (&issueFilter{boardID: 1, limit: -1}).validate(); err == nil {
		t.Error("negative limit accepted")
	}
	if err := (&issueFilter{boardID: 1}).validate(); err != nil {
		t.Errorf("valid filter rejected: %v", err)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("abcdefghijk", 8); got != "abcde..." {
		t.Errorf("truncate long = %q", got)
	}
}

func TestSprintJQL(t *testing.T) {
	if got := sprintJQL(10, ""); got != "sprint = 10 ORDER BY Rank ASC" {
		t.Errorf("sprintJQL empty = %q", got)
	}
	if got := sprintJQL(10, "statusCategory != Done"); got != "sprint = 10 AND statusCategory != Done ORDER BY Rank ASC" {
		t.Errorf("sprintJQL filtered = %q", got)
	}
}

func TestMatchesSprint(t *testing.T) {
	s := jira.Sprint{ID: 10, Name: "Support"}
	for _, ref := range []string{"10", "Support", "support", " support "} {
		if !matchesSprint(s, ref) {
			t.Errorf("matchesSprint(%q) = false", ref)
		}
	}
	for _, ref := range []string{"1", "Sup", "Sprint 1"} {
		if matchesSprint(s, ref) {
			t.Errorf("matchesSprint(%q) = true", ref)
		}
	}
}

func TestReadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "# comment\n\nJIRA_URL=https://x.atlassian.net\nexport JIRA_SPRINT=\"Support\"\nJIRA_EMAIL='me@example.com'\nJIRA_API_TOKEN=a=b\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readDotEnv(path)
	if err != nil {
		t.Fatalf("readDotEnv: %v", err)
	}
	want := map[string]string{
		"JIRA_URL":       "https://x.atlassian.net",
		"JIRA_SPRINT":    "Support",
		"JIRA_EMAIL":     "me@example.com",
		"JIRA_API_TOKEN": "a=b",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d keys, want %d", len(got), len(want))
	}
}

func TestReadDotEnvMissingFile(t *testing.T) {
	got, err := readDotEnv(filepath.Join(t.TempDir(), "missing"))
	if err != nil || len(got) != 0 {
		t.Fatalf("readDotEnv missing = %v, %v", got, err)
	}
}

func TestReadDotEnvRejectsMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("JIRA_URL\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDotEnv(path); err == nil {
		t.Fatal("malformed line accepted")
	}
}

func TestReadDotEnvsLaterFileWins(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "config.env")
	local := filepath.Join(dir, ".env")
	if err := os.WriteFile(user, []byte("JIRA_URL=https://x.atlassian.net\nJIRA_SPRINT=Support\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("JIRA_SPRINT=Sprint 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readDotEnvs(user, local, filepath.Join(dir, "missing"))
	if err != nil {
		t.Fatalf("readDotEnvs: %v", err)
	}
	if got["JIRA_URL"] != "https://x.atlassian.net" || got["JIRA_SPRINT"] != "Sprint 1" {
		t.Errorf("readDotEnvs = %v", got)
	}
}

func TestUserConfigFile(t *testing.T) {
	env := map[string]string{"XDG_CONFIG_HOME": "/xdg"}
	got, err := userConfigFile(func(k string) string { return env[k] })
	if err != nil || got != filepath.Join("/xdg", "jira-cli", "config.env") {
		t.Errorf("userConfigFile with XDG = %q, %v", got, err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	got, err = userConfigFile(func(string) string { return "" })
	if err != nil || got != filepath.Join(home, ".config", "jira-cli", "config.env") {
		t.Errorf("userConfigFile = %q, %v", got, err)
	}
}

func TestPrintIssueComments(t *testing.T) {
	issue := jira.Issue{
		Key:    "PROJ-123",
		Fields: jira.IssueFields{Summary: "Fix it", Description: "desc"},
		Comments: []jira.Comment{
			{Author: &jira.User{DisplayName: "Ann"}, Body: "first"},
			{Body: "second"},
		},
	}
	var out strings.Builder
	if err := printIssue(&out, issue, "https://site/browse/PROJ-123"); err != nil {
		t.Fatal(err)
	}
	want := "desc\n\nComments (2)\n\nAnn  -\nfirst\n\n-  -\nsecond\n"
	if !strings.HasSuffix(out.String(), want) {
		t.Errorf("output ends with:\n%s\nwant suffix:\n%s", out.String(), want)
	}
}

func TestIssueKey(t *testing.T) {
	tests := map[string]string{
		"PROJ-123": "PROJ-123",
		"https://your-domain.atlassian.net/browse/PROJ-123":                                             "PROJ-123",
		"https://your-domain.atlassian.net/browse/PROJ-123/":                                            "PROJ-123",
		"https://your-domain.atlassian.net/browse/PROJ-123?focusedCommentId=1":                          "PROJ-123",
		"https://your-domain.atlassian.net/jira/software/projects/PROJ/boards/7?selectedIssue=PROJ-123": "PROJ-123",
	}
	for ref, want := range tests {
		got, err := issueKey(ref)
		if err != nil || got != want {
			t.Errorf("issueKey(%q) = %q, %v, want %q", ref, got, err, want)
		}
	}
	for _, ref := range []string{"https://your-domain.atlassian.net/browse/", "https://your-domain.atlassian.net/jira/your-work"} {
		if got, err := issueKey(ref); err == nil {
			t.Errorf("issueKey(%q) = %q, want error", ref, got)
		}
	}
}

func TestEnvLookupPrefersEnvironment(t *testing.T) {
	t.Setenv("JIRA_SPRINT", "Sprint 1")
	lookup := envLookup(map[string]string{"JIRA_SPRINT": "Support", "JIRA_BOARD_ID": "1"})
	if got := lookup("JIRA_SPRINT"); got != "Sprint 1" {
		t.Errorf("JIRA_SPRINT = %q", got)
	}
	if got := lookup("JIRA_BOARD_ID"); got != "1" {
		t.Errorf("JIRA_BOARD_ID = %q", got)
	}
}

func TestIssueColumns(t *testing.T) {
	browse := func(key string) string { return "https://site/browse/" + key }
	headers := func(cols []issueColumn) string {
		names := make([]string, 0, len(cols))
		for _, c := range cols {
			names = append(names, c.header)
		}
		return strings.Join(names, " ")
	}
	if got := headers((&issueFilter{}).columns(browse)); got != "KEY STATUS UPDATED URL SUMMARY" {
		t.Errorf("default columns = %q", got)
	}
	if got := headers((&issueFilter{wide: true}).columns(browse)); got != "KEY TYPE STATUS PRIORITY ASSIGNEE UPDATED URL SUMMARY" {
		t.Errorf("wide columns = %q", got)
	}

	var out strings.Builder
	issues := []jira.Issue{{Key: "PROJ-1", Fields: jira.IssueFields{Summary: "Fix", Status: &jira.Named{Name: "To Do"}}}}
	if err := printIssues(&out, issues, defaultColumns(browse)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "https://site/browse/PROJ-1") || strings.Contains(out.String(), "ASSIGNEE") {
		t.Errorf("output = %q", out.String())
	}
}
