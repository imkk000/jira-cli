package jira

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func newTestClient(t *testing.T, email string, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL, email, "secret")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func respond(t *testing.T, w io.Writer, body string) {
	t.Helper()
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func TestBoardIssuesPaginatesWithBasicAuth(t *testing.T) {
	c := newTestClient(t, "me@example.com", func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "me@example.com" || pass != "secret" {
			t.Errorf("basic auth = %q %q %v", user, pass, ok)
		}
		if r.URL.Path != "/rest/agile/1.0/board/7/issue" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("jql"); got != `status = "Done"` {
			t.Errorf("jql = %q", got)
		}
		switch start := r.URL.Query().Get("startAt"); start {
		case "0":
			respond(t, w, `{"total":3,"issues":[
				{"key":"A-1","fields":{"summary":"one","updated":"2026-10-01T09:30:00.000+0700","status":{"name":"Done"}}},
				{"key":"A-2","fields":{"summary":"two"}}]}`)
		case "2":
			respond(t, w, `{"total":3,"issues":[{"key":"A-3","fields":{"summary":"three"}}]}`)
		default:
			t.Errorf("unexpected startAt %q", start)
		}
	})

	issues, err := c.BoardIssues(t.Context(), 7, `status = "Done"`, 0)
	if err != nil {
		t.Fatalf("BoardIssues: %v", err)
	}
	keys := make([]string, 0, len(issues))
	for _, i := range issues {
		keys = append(keys, i.Key)
	}
	if !slices.Equal(keys, []string{"A-1", "A-2", "A-3"}) {
		t.Fatalf("keys = %v", keys)
	}
	if got := issues[0].Fields.Updated.UTC().Format("2006-01-02 15:04"); got != "2026-10-01 02:30" {
		t.Errorf("updated = %q", got)
	}
	if got := issues[0].Fields.Status.String(); got != "Done" {
		t.Errorf("status = %q", got)
	}
	if got := issues[1].Fields.Assignee.String(); got != "-" {
		t.Errorf("nil assignee = %q", got)
	}
}

func TestBoardIssuesRespectsLimit(t *testing.T) {
	c := newTestClient(t, "me@example.com", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("maxResults"); got != "1" {
			t.Errorf("maxResults = %q", got)
		}
		respond(t, w, `{"total":10,"issues":[{"key":"A-1","fields":{}}]}`)
	})

	issues, err := c.BoardIssues(t.Context(), 1, "", 1)
	if err != nil {
		t.Fatalf("BoardIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("len = %d", len(issues))
	}
}

func TestBoardsStopsOnIsLastWithBearerAuth(t *testing.T) {
	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.URL.Query().Get("name"); got != "team" {
			t.Errorf("name = %q", got)
		}
		respond(t, w, `{"isLast":true,"values":[{"id":42,"name":"Team board","type":"scrum","location":{"projectKey":"TEAM"}}]}`)
	})

	boards, err := c.Boards(t.Context(), "team")
	if err != nil {
		t.Fatalf("Boards: %v", err)
	}
	if len(boards) != 1 || boards[0].ID != 42 || boards[0].Location.ProjectKey != "TEAM" {
		t.Fatalf("boards = %+v", boards)
	}
}

func TestAPIErrorIncludesJiraMessages(t *testing.T) {
	c := newTestClient(t, "me@example.com", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		respond(t, w, `{"errorMessages":["The board does not support sprints"],"errors":{"jql":"bad field"}}`)
	})

	_, err := c.Sprints(t.Context(), 1, "active")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d", apiErr.StatusCode)
	}
	for _, want := range []string{"does not support sprints", "jql: bad field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestIssueRejectsInvalidKey(t *testing.T) {
	c := newTestClient(t, "me@example.com", func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})

	for _, key := range []string{"", "PROJ", "../secret", "PROJ-1/comment"} {
		if _, err := c.Issue(t.Context(), key); err == nil {
			t.Errorf("Issue(%q) succeeded", key)
		}
	}
}

func TestIssueFetchesDetail(t *testing.T) {
	c := newTestClient(t, "me@example.com", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/issue/PROJ-12" {
			t.Errorf("path = %q", r.URL.Path)
		}
		respond(t, w, `{"key":"PROJ-12","fields":{"summary":"Fix it","description":"plain text","labels":["a"]}}`)
	})

	issue, err := c.Issue(t.Context(), "proj-12")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if issue.Fields.Description != "plain text" || issue.Fields.Summary != "Fix it" {
		t.Fatalf("issue = %+v", issue)
	}
	browse, err := c.BrowseURLs(t.Context())
	if err != nil {
		t.Fatalf("BrowseURLs: %v", err)
	}
	if got := browse(issue.Key); !strings.HasSuffix(got, "/browse/PROJ-12") {
		t.Errorf("browse url = %q", got)
	}
}

func TestCommentsPaginatesOldestFirst(t *testing.T) {
	c := newTestClient(t, "me@example.com", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/issue/PROJ-12/comment" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("orderBy"); got != "created" {
			t.Errorf("orderBy = %q", got)
		}
		switch start := r.URL.Query().Get("startAt"); start {
		case "0":
			respond(t, w, `{"total":2,"comments":[{"id":"1","author":{"displayName":"Ann"},"body":"first","created":"2026-10-01T09:30:00.000+0700"}]}`)
		case "1":
			respond(t, w, `{"total":2,"comments":[{"id":"2","author":{"displayName":"Bob"},"body":"second"}]}`)
		default:
			t.Errorf("unexpected startAt %q", start)
		}
	})

	comments, err := c.Comments(t.Context(), "PROJ-12")
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(comments) != 2 || comments[0].Body != "first" || comments[1].Author.String() != "Bob" {
		t.Fatalf("comments = %+v", comments)
	}
}

func TestTextDecodesADF(t *testing.T) {
	doc := `{"type":"doc","content":[
		{"type":"paragraph","content":[{"type":"text","text":"Hello "},{"type":"text","text":"world"}]},
		{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"item"}]}]}]}]}`
	var text Text
	if err := json.Unmarshal([]byte(doc), &text); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if want := "Hello world\n- item"; string(text) != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

func TestNewClientValidatesInput(t *testing.T) {
	cases := []struct{ url, token string }{
		{"", "t"},
		{"your-domain.atlassian.net", "t"},
		{"ftp://example.com", "t"},
		{"https://example.atlassian.net", ""},
	}
	for _, tc := range cases {
		if _, err := NewClient(tc.url, "", tc.token); err == nil {
			t.Errorf("NewClient(%q, %q) succeeded", tc.url, tc.token)
		}
	}
}

func TestSprintsSendsStates(t *testing.T) {
	c := newTestClient(t, "me@example.com", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("state"); got != "active,future" {
			t.Errorf("state = %q", got)
		}
		respond(t, w, `{"isLast":true,"values":[{"id":10,"name":"Support","state":"future"}]}`)
	})

	sprints, err := c.Sprints(t.Context(), 1, "active", "future")
	if err != nil {
		t.Fatalf("Sprints: %v", err)
	}
	if len(sprints) != 1 || sprints[0].ID != 10 || sprints[0].Name != "Support" {
		t.Fatalf("sprints = %+v", sprints)
	}
}

func TestSearchIssuesFollowsNextPageToken(t *testing.T) {
	c := newTestClient(t, "me@example.com", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/search/jql" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("jql"); got != "sprint = 10" {
			t.Errorf("jql = %q", got)
		}
		switch token := r.URL.Query().Get("nextPageToken"); token {
		case "":
			respond(t, w, `{"issues":[{"key":"A-1","fields":{}},{"key":"A-2","fields":{}}],"nextPageToken":"p2"}`)
		case "p2":
			respond(t, w, `{"issues":[{"key":"A-3","fields":{}}],"isLast":true}`)
		default:
			t.Errorf("unexpected nextPageToken %q", token)
		}
	})

	issues, err := c.SearchIssues(t.Context(), "sprint = 10", 0)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(issues) != 3 || issues[2].Key != "A-3" {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestSearchIssuesRespectsLimit(t *testing.T) {
	c := newTestClient(t, "me@example.com", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("maxResults"); got != "2" {
			t.Errorf("maxResults = %q", got)
		}
		respond(t, w, `{"issues":[{"key":"A-1","fields":{}},{"key":"A-2","fields":{}}],"nextPageToken":"more"}`)
	})

	issues, err := c.SearchIssues(t.Context(), "sprint = 1", 2)
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("len = %d", len(issues))
	}
}

func TestTransitionsAndTransitionIssue(t *testing.T) {
	c := newTestClient(t, "me@example.com", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/issue/PROJ-9/transitions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		switch r.Method {
		case http.MethodGet:
			respond(t, w, `{"transitions":[{"id":"2","name":"Sent to review","to":{"name":"In Code Review"}}]}`)
		case http.MethodPost:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read body: %v", err)
			}
			if string(body) != `{"transition":{"id":"2"}}` {
				t.Errorf("body = %s", body)
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})

	transitions, err := c.Transitions(t.Context(), "proj-9")
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	if len(transitions) != 1 || transitions[0].To.Name != "In Code Review" {
		t.Fatalf("transitions = %+v", transitions)
	}
	if err := c.TransitionIssue(t.Context(), "PROJ-9", "2"); err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	if err := c.TransitionIssue(t.Context(), "../x", "2"); err == nil {
		t.Error("invalid key accepted")
	}
}

func TestBrowseURLsResolvesGatewaySite(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/ex/jira/cloud-1/rest/api/2/serverInfo" {
			t.Errorf("path = %q", r.URL.Path)
		}
		respond(t, w, `{"baseUrl":"https://example.atlassian.net"}`)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL+"/ex/jira/cloud-1", "me@example.com", "secret")
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		browse, err := c.BrowseURLs(t.Context())
		if err != nil {
			t.Fatalf("BrowseURLs: %v", err)
		}
		if got := browse("PROJ-1"); got != "https://example.atlassian.net/browse/PROJ-1" {
			t.Errorf("browse = %q", got)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("serverInfo calls = %d, want 1", n)
	}
}

func TestUseSiteURLSkipsLookup(t *testing.T) {
	c := newTestClient(t, "me@example.com", func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})
	if err := c.UseSiteURL("https://example.atlassian.net/"); err != nil {
		t.Fatal(err)
	}
	browse, err := c.BrowseURLs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := browse("PROJ-2"); got != "https://example.atlassian.net/browse/PROJ-2" {
		t.Errorf("browse = %q", got)
	}
	if err := c.UseSiteURL("example.atlassian.net"); err == nil {
		t.Error("invalid site url accepted")
	}
}
