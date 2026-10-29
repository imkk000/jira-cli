package jira

import (
	"encoding/json"
	"fmt"
	"time"
)

type Issue struct {
	ID     string      `json:"id"`
	Key    string      `json:"key"`
	Fields IssueFields `json:"fields"`
}

type IssueFields struct {
	Summary     string   `json:"summary"`
	Description Text     `json:"description,omitempty"`
	Status      *Named   `json:"status,omitempty"`
	IssueType   *Named   `json:"issuetype,omitempty"`
	Priority    *Named   `json:"priority,omitempty"`
	Assignee    *User    `json:"assignee,omitempty"`
	Reporter    *User    `json:"reporter,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Created     Time     `json:"created,omitzero"`
	Updated     Time     `json:"updated,omitzero"`
}

type Named struct {
	Name string `json:"name"`
}

func (n *Named) String() string {
	if n == nil || n.Name == "" {
		return "-"
	}
	return n.Name
}

type User struct {
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress,omitempty"`
}

func (u *User) String() string {
	if u == nil || u.DisplayName == "" {
		return "-"
	}
	return u.DisplayName
}

var timeLayouts = []string{"2006-01-02T15:04:05.000-0700", time.RFC3339}

type Time struct {
	time.Time
}

func (t *Time) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s == "" {
		return nil
	}
	for _, layout := range timeLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			t.Time = parsed
			return nil
		}
	}
	return fmt.Errorf("unrecognized jira time %q", s)
}
