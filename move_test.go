package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/imkk000/jira-cli/internal/jira"
)

type fakeJira struct {
	t           *testing.T
	inSprint    bool
	postStatus  int
	mu          sync.Mutex
	postedBody  []string
	searchedJQL []string
}

func (f *fakeJira) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	write := func(body string) {
		if _, err := io.WriteString(w, body); err != nil {
			f.t.Errorf("write response: %v", err)
		}
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/PROJ-1":
		write(`{"key":"PROJ-1","fields":{"summary":"Fix login","status":{"name":"In Progress"}}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/PROJ-1/transitions":
		write(`{"transitions":[
			{"id":"2","name":"Sent to review","to":{"name":"In Code Review"}},
			{"id":"18","name":"Block","to":{"name":"Block"}}]}`)
	case r.Method == http.MethodPost && r.URL.Path == "/rest/api/2/issue/PROJ-1/transitions":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Errorf("read body: %v", err)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			f.t.Errorf("content-type = %q", got)
		}
		f.mu.Lock()
		f.postedBody = append(f.postedBody, string(body))
		f.mu.Unlock()
		if f.postStatus != 0 {
			w.WriteHeader(f.postStatus)
			write(`{"code":401,"message":"Unauthorized; scope does not match"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/rest/agile/1.0/board/1/sprint":
		write(`{"isLast":true,"values":[{"id":10,"name":"Support","state":"future"}]}`)
	case r.URL.Path == "/rest/api/2/search/jql":
		f.mu.Lock()
		f.searchedJQL = append(f.searchedJQL, r.URL.Query().Get("jql"))
		f.mu.Unlock()
		if f.inSprint {
			write(`{"issues":[{"key":"PROJ-1","fields":{}}]}`)
		} else {
			write(`{"issues":[]}`)
		}
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeJira) posts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.postedBody...)
}

func runMoveAgainst(t *testing.T, fake *fakeJira, input string, args ...string) (string, error) {
	t.Helper()
	fake.t = t
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client, err := jira.NewClient(srv.URL, "me@example.com", "secret")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{boardID: 1, sprint: "Support"}
	var out bytes.Buffer
	err = runMove(t.Context(), client, cfg, args, strings.NewReader(input), &out)
	return out.String(), err
}

func TestMoveDryRunDoesNotPost(t *testing.T) {
	fake := &fakeJira{inSprint: true}
	out, err := runMoveAgainst(t, fake, "", "-dry-run", "PROJ-1", "In Code Review")
	if err != nil {
		t.Fatalf("runMove: %v", err)
	}
	if !strings.Contains(out, "In Progress -> In Code Review (Sent to review)") {
		t.Errorf("output = %q", out)
	}
	if len(fake.posts()) != 0 {
		t.Errorf("dry run posted %v", fake.posts())
	}
}

func TestMoveDeclinedDoesNotPost(t *testing.T) {
	fake := &fakeJira{inSprint: true}
	out, err := runMoveAgainst(t, fake, "n\n", "PROJ-1", "In Code Review")
	if err != nil {
		t.Fatalf("runMove: %v", err)
	}
	if !strings.Contains(out, "Cancelled.") || len(fake.posts()) != 0 {
		t.Errorf("output = %q, posts = %v", out, fake.posts())
	}
}

func TestMoveEOFDoesNotPost(t *testing.T) {
	fake := &fakeJira{inSprint: true}
	if _, err := runMoveAgainst(t, fake, "", "PROJ-1", "In Code Review"); err != nil {
		t.Fatalf("runMove: %v", err)
	}
	if len(fake.posts()) != 0 {
		t.Errorf("posted without confirmation: %v", fake.posts())
	}
}

func TestMoveConfirmedPostsTransition(t *testing.T) {
	fake := &fakeJira{inSprint: true}
	out, err := runMoveAgainst(t, fake, "y\n", "proj-1", "in code review")
	if err != nil {
		t.Fatalf("runMove: %v", err)
	}
	if posts := fake.posts(); len(posts) != 1 || posts[0] != `{"transition":{"id":"2"}}` {
		t.Fatalf("posts = %v", posts)
	}
	if !strings.Contains(out, "Moved PROJ-1 to In Code Review.") {
		t.Errorf("output = %q", out)
	}
	if jql := fake.searchedJQL; len(jql) != 1 || jql[0] != "key = PROJ-1 AND sprint = 10" {
		t.Errorf("sprint check jql = %v", jql)
	}
}

func TestMoveYesFlagAfterUnquotedStatus(t *testing.T) {
	fake := &fakeJira{inSprint: true}
	if _, err := runMoveAgainst(t, fake, "", "PROJ-1", "In", "Code", "Review", "-yes"); err != nil {
		t.Fatalf("runMove: %v", err)
	}
	if len(fake.posts()) != 1 {
		t.Errorf("posts = %v", fake.posts())
	}
}

func TestMoveRefusesIssueOutsideDefaultSprint(t *testing.T) {
	fake := &fakeJira{}
	_, err := runMoveAgainst(t, fake, "", "-yes", "PROJ-1", "In Code Review")
	if err == nil || !strings.Contains(err.Error(), `not in sprint "Support"`) {
		t.Fatalf("err = %v", err)
	}
	if len(fake.posts()) != 0 {
		t.Errorf("posted %v", fake.posts())
	}
}

func TestMoveAnySprintSkipsSprintCheck(t *testing.T) {
	fake := &fakeJira{}
	if _, err := runMoveAgainst(t, fake, "", "-yes", "-any-sprint", "PROJ-1", "Block"); err != nil {
		t.Fatalf("runMove: %v", err)
	}
	if posts := fake.posts(); len(posts) != 1 || posts[0] != `{"transition":{"id":"18"}}` {
		t.Errorf("posts = %v", posts)
	}
}

func TestMoveUnreachableStatusListsOptions(t *testing.T) {
	fake := &fakeJira{inSprint: true}
	_, err := runMoveAgainst(t, fake, "", "-yes", "PROJ-1", "Done")
	if err == nil || !strings.Contains(err.Error(), "cannot move straight to \"Done\"") || !strings.Contains(err.Error(), "In Code Review") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.posts()) != 0 {
		t.Errorf("posted %v", fake.posts())
	}
}

func TestMoveAlreadyInStatus(t *testing.T) {
	fake := &fakeJira{inSprint: true}
	out, err := runMoveAgainst(t, fake, "", "-yes", "PROJ-1", "in progress")
	if err != nil {
		t.Fatalf("runMove: %v", err)
	}
	if !strings.Contains(out, "already in In Progress") || len(fake.posts()) != 0 {
		t.Errorf("output = %q, posts = %v", out, fake.posts())
	}
}

func TestMoveUnauthorizedHintsScope(t *testing.T) {
	fake := &fakeJira{inSprint: true, postStatus: http.StatusUnauthorized}
	_, err := runMoveAgainst(t, fake, "", "-yes", "PROJ-1", "In Code Review")
	if err == nil || !strings.Contains(err.Error(), "write:jira-work") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindTransition(t *testing.T) {
	transitions := []jira.Transition{
		{ID: "5", Name: "Approve", To: jira.Named{Name: "Ready to Deploy"}},
		{ID: "11", Name: "Reject", To: jira.Named{Name: "In Progress"}},
		{ID: "18", Name: "Block", To: jira.Named{Name: "Block"}},
	}
	for target, wantID := range map[string]string{
		"ready to deploy": "5",
		"Approve":         "5",
		"11":              "11",
		"block":           "18",
	} {
		got, err := findTransition(transitions, target)
		if err != nil || got.ID != wantID {
			t.Errorf("findTransition(%q) = %v, %v, want ID %s", target, got.ID, err, wantID)
		}
	}
	if _, err := findTransition(transitions, "Done"); err == nil {
		t.Error("unreachable status accepted")
	}

	ambiguous := []jira.Transition{
		{ID: "1", Name: "Fast", To: jira.Named{Name: "Done"}},
		{ID: "2", Name: "Slow", To: jira.Named{Name: "Done"}},
	}
	if _, err := findTransition(ambiguous, "Done"); err == nil || !strings.Contains(err.Error(), "several") {
		t.Errorf("ambiguous err = %v", err)
	}
	if got, err := findTransition(ambiguous, "2"); err != nil || got.ID != "2" {
		t.Errorf("by ID = %v, %v", got.ID, err)
	}
}

func TestParseInterleaved(t *testing.T) {
	fs, f := newIssueFlags("x", 0)
	got, err := parseInterleaved(fs, []string{"PROJ-1", "-json", "In", "Code", "Review"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "PROJ-1|In|Code|Review" || !f.json {
		t.Errorf("positional = %v, json = %v", got, f.json)
	}
}
