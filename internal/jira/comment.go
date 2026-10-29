package jira

type Comment struct {
	ID      string `json:"id"`
	Author  *User  `json:"author,omitempty"`
	Body    Text   `json:"body"`
	Created Time   `json:"created,omitzero"`
	Updated Time   `json:"updated,omitzero"`
}
