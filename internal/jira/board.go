package jira

import "time"

type Board struct {
	ID       int           `json:"id"`
	Name     string        `json:"name"`
	Type     string        `json:"type"`
	Location BoardLocation `json:"location,omitzero"`
}

type BoardLocation struct {
	ProjectKey  string `json:"projectKey,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

type Sprint struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Goal      string    `json:"goal,omitempty"`
	StartDate time.Time `json:"startDate,omitzero"`
	EndDate   time.Time `json:"endDate,omitzero"`
}
