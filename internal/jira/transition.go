package jira

type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   Named  `json:"to"`
}

type transitionRequest struct {
	Transition transitionRef `json:"transition"`
}

type transitionRef struct {
	ID string `json:"id"`
}
