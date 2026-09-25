package fabric

type ActionType string

const (
	ActionRefresh  ActionType = "refresh"
	ActionNavigate ActionType = "navigate"
	ActionBack     ActionType = "back"
	ActionInvoke   ActionType = "invoke"
)

type Action struct {
	Type ActionType     `json:"type"`
	Page string         `json:"page,omitempty"`
	Name string         `json:"name,omitempty"`
	Args map[string]any `json:"args,omitempty"`
}
