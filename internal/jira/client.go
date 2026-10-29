package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	pageSize          = 50
	searchPageSize    = 100
	gatewayPathPrefix = "/ex/jira/"
)

var (
	listFields      = []string{"summary", "status", "issuetype", "priority", "assignee", "updated"}
	detailFields    = []string{"summary", "description", "status", "issuetype", "priority", "assignee", "reporter", "labels", "created", "updated"}
	issueKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-[0-9]+$`)
)

type Client struct {
	baseURL   *url.URL
	siteURL   *url.URL
	http      *http.Client
	authorize func(*http.Request)
}

func NewClient(baseURL, email, token string) (*Client, error) {
	u, err := parseSiteURL(baseURL)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, errors.New("jira api token is empty")
	}
	c := &Client{baseURL: u, http: &http.Client{Timeout: 30 * time.Second}}
	if email != "" {
		c.authorize = func(r *http.Request) { r.SetBasicAuth(email, token) }
	} else {
		c.authorize = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	}
	return c, nil
}

func parseSiteURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse jira url: %w", err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("invalid jira url %q, want e.g. https://your-domain.atlassian.net", raw)
	}
	return u, nil
}

func (c *Client) UseSiteURL(raw string) error {
	u, err := parseSiteURL(raw)
	if err != nil {
		return err
	}
	c.siteURL = u
	return nil
}

func (c *Client) BrowseURLs(ctx context.Context) (func(key string) string, error) {
	site, err := c.site(ctx)
	if err != nil {
		return nil, err
	}
	return func(key string) string { return site.JoinPath("browse", key).String() }, nil
}

func (c *Client) Boards(ctx context.Context, name string) ([]Board, error) {
	q := make(url.Values)
	if name != "" {
		q.Set("name", name)
	}
	return paginate[Board](ctx, c, "rest/agile/1.0/board", q, 0)
}

func (c *Client) BoardIssues(ctx context.Context, boardID int, jql string, limit int) ([]Issue, error) {
	path := fmt.Sprintf("rest/agile/1.0/board/%d/issue", boardID)
	return paginate[Issue](ctx, c, path, issueQuery(jql), limit)
}

func (c *Client) Sprints(ctx context.Context, boardID int, states ...string) ([]Sprint, error) {
	q := make(url.Values)
	if len(states) > 0 {
		q.Set("state", strings.Join(states, ","))
	}
	return paginate[Sprint](ctx, c, fmt.Sprintf("rest/agile/1.0/board/%d/sprint", boardID), q, 0)
}

func (c *Client) SearchIssues(ctx context.Context, jql string, limit int) ([]Issue, error) {
	var all []Issue
	var nextPageToken string
	for {
		q := issueQuery(jql)
		size := searchPageSize
		if limit > 0 {
			size = min(size, limit-len(all))
		}
		q.Set("maxResults", strconv.Itoa(size))
		if nextPageToken != "" {
			q.Set("nextPageToken", nextPageToken)
		}

		var p struct {
			Issues        []Issue `json:"issues"`
			NextPageToken string  `json:"nextPageToken"`
		}
		if err := c.get(ctx, "rest/api/2/search/jql", q, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Issues...)
		nextPageToken = p.NextPageToken

		if nextPageToken == "" || len(p.Issues) == 0 || (limit > 0 && len(all) >= limit) {
			return all, nil
		}
	}
}

func (c *Client) Issue(ctx context.Context, key string) (Issue, error) {
	path, err := issuePath(key)
	if err != nil {
		return Issue{}, err
	}
	q := make(url.Values)
	q.Set("fields", strings.Join(detailFields, ","))
	var issue Issue
	if err := c.get(ctx, path, q, &issue); err != nil {
		return Issue{}, err
	}
	return issue, nil
}

func (c *Client) Comments(ctx context.Context, key string) ([]Comment, error) {
	path, err := issuePath(key, "comment")
	if err != nil {
		return nil, err
	}
	q := make(url.Values)
	q.Set("orderBy", "created")
	return paginate[Comment](ctx, c, path, q, 0)
}

func (c *Client) Transitions(ctx context.Context, key string) ([]Transition, error) {
	path, err := issuePath(key, "transitions")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Transitions []Transition `json:"transitions"`
	}
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Transitions, nil
}

func (c *Client) TransitionIssue(ctx context.Context, key, transitionID string) error {
	path, err := issuePath(key, "transitions")
	if err != nil {
		return err
	}
	body := transitionRequest{Transition: transitionRef{ID: transitionID}}
	return c.do(ctx, http.MethodPost, path, nil, body, nil)
}

func (c *Client) site(ctx context.Context) (*url.URL, error) {
	if c.siteURL != nil {
		return c.siteURL, nil
	}
	if !strings.HasPrefix(c.baseURL.Path, gatewayPathPrefix) {
		return c.baseURL, nil
	}
	var info struct {
		BaseURL string `json:"baseUrl"`
	}
	if err := c.get(ctx, "rest/api/2/serverInfo", nil, &info); err != nil {
		return nil, fmt.Errorf("look up site url (or set JIRA_SITE_URL): %w", err)
	}
	u, err := parseSiteURL(info.BaseURL)
	if err != nil {
		return nil, err
	}
	c.siteURL = u
	return u, nil
}

func issuePath(key string, rest ...string) (string, error) {
	if !issueKeyPattern.MatchString(key) {
		return "", fmt.Errorf("invalid issue key %q, want e.g. PROJ-123", key)
	}
	return strings.Join(append([]string{"rest/api/2/issue", strings.ToUpper(key)}, rest...), "/"), nil
}

func issueQuery(jql string) url.Values {
	q := make(url.Values)
	q.Set("fields", strings.Join(listFields, ","))
	if jql != "" {
		q.Set("jql", jql)
	}
	return q
}

type page[T any] struct {
	Total    int  `json:"total"`
	IsLast   bool `json:"isLast"`
	Values   []T  `json:"values"`
	Issues   []T  `json:"issues"`
	Comments []T  `json:"comments"`
}

func paginate[T any](ctx context.Context, c *Client, path string, query url.Values, limit int) ([]T, error) {
	var all []T
	var startAt int
	for {
		size := pageSize
		if limit > 0 {
			size = min(size, limit-len(all))
		}
		q := maps.Clone(query)
		q.Set("startAt", strconv.Itoa(startAt))
		q.Set("maxResults", strconv.Itoa(size))

		var p page[T]
		if err := c.get(ctx, path, q, &p); err != nil {
			return nil, err
		}
		items := slices.Concat(p.Values, p.Issues, p.Comments)
		all = append(all, items...)
		startAt += len(items)

		done := len(items) == 0 || p.IsLast || (p.Total > 0 && startAt >= p.Total) || (limit > 0 && len(all) >= limit)
		if done {
			return all, nil
		}
	}
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.baseURL.JoinPath(path)
	u.RawQuery = query.Encode()

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s: %w", path, err)
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.authorize(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return newAPIError(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

type APIError struct {
	StatusCode int
	Messages   []string
}

func (e *APIError) Error() string {
	status := fmt.Sprintf("jira returned %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	if len(e.Messages) == 0 {
		return status
	}
	return status + ": " + strings.Join(e.Messages, "; ")
}

func newAPIError(resp *http.Response) error {
	apiErr := &APIError{StatusCode: resp.StatusCode}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return apiErr
	}
	var payload struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
		Message       string            `json:"message"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return apiErr
	}
	apiErr.Messages = append(apiErr.Messages, payload.ErrorMessages...)
	for _, field := range slices.Sorted(maps.Keys(payload.Errors)) {
		apiErr.Messages = append(apiErr.Messages, field+": "+payload.Errors[field])
	}
	if payload.Message != "" {
		apiErr.Messages = append(apiErr.Messages, payload.Message)
	}
	return apiErr
}
