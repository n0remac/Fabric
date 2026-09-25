package fabric

type ComponentType string

const (
	ComponentColumn   ComponentType = "column"
	ComponentRow      ComponentType = "row"
	ComponentCard     ComponentType = "card"
	ComponentText     ComponentType = "text"
	ComponentMetric   ComponentType = "metric"
	ComponentDivider  ComponentType = "divider"
	ComponentList     ComponentType = "list"
	ComponentProgress ComponentType = "progress"
	ComponentButton   ComponentType = "button"
)

var ComponentTypes = []ComponentType{
	ComponentColumn, ComponentRow, ComponentCard, ComponentText, ComponentMetric,
	ComponentDivider, ComponentList, ComponentProgress, ComponentButton,
}

type StyleToken string

const (
	StyleHeading    StyleToken = "heading"
	StyleSubheading StyleToken = "subheading"
	StyleBody       StyleToken = "body"
	StyleMuted      StyleToken = "muted"
	StyleEmphasis   StyleToken = "emphasis"
)

type ValueFormat string

const (
	FormatPlain    ValueFormat = "plain"
	FormatDuration ValueFormat = "duration"
)

type Component struct {
	Type     ComponentType `json:"type"`
	ID       string        `json:"id,omitempty"`
	Label    string        `json:"label,omitempty"`
	Text     string        `json:"text,omitempty"`
	Bind     string        `json:"bind,omitempty"`
	Style    StyleToken    `json:"style,omitempty"`
	Format   ValueFormat   `json:"format,omitempty"`
	Suffix   string        `json:"suffix,omitempty"`
	Children []Component   `json:"children,omitempty"`
	Action   *Action       `json:"action,omitempty"`
}

func Walk(component Component, visit func(Component) bool) bool {
	if !visit(component) {
		return false
	}
	for _, child := range component.Children {
		if !Walk(child, visit) {
			return false
		}
	}
	return true
}

func FindComponent(root Component, id string) (Component, bool) {
	var found Component
	ok := false
	Walk(root, func(component Component) bool {
		if component.ID == id {
			found, ok = component, true
			return false
		}
		return true
	})
	return found, ok
}
