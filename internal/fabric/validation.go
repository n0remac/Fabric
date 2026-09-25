package fabric

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/n0remac/Fabric/schemas"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	maxComponentDepth = 32
	maxComponents     = 512
)

type RegistryLookup interface {
	Has(string) bool
}

type ValidationIssue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ValidationError struct {
	Issues []ValidationIssue `json:"issues"`
}

func (e *ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "page validation failed"
	}
	return fmt.Sprintf("page validation failed at %s: %s", e.Issues[0].Path, e.Issues[0].Message)
}

type Validator struct {
	schema    *jsonschema.Schema
	actions   RegistryLookup
	providers RegistryLookup
}

func NewValidator(actions, providers RegistryLookup) (*Validator, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemas.FabricPageV01))
	if err != nil {
		return nil, fmt.Errorf("decode embedded page schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://fabric.local/schemas/fabric-page-v0.1.json"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		return nil, fmt.Errorf("add page schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("compile page schema: %w", err)
	}
	return &Validator{schema: compiled, actions: actions, providers: providers}, nil
}

func (v *Validator) Decode(data []byte) (Page, error) {
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return Page{}, &ValidationError{Issues: []ValidationIssue{{Path: "/", Code: "invalid_json", Message: err.Error()}}}
	}
	if err := v.schema.Validate(instance); err != nil {
		return Page{}, schemaValidationError(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var page Page
	if err := decoder.Decode(&page); err != nil {
		return Page{}, fmt.Errorf("decode validated page: %w", err)
	}
	issues := v.validateSemantics(page)
	if len(issues) > 0 {
		return Page{}, &ValidationError{Issues: issues}
	}
	return page, nil
}

func (v *Validator) ValidateReferences(page Page, pages map[string]Page) error {
	var issues []ValidationIssue
	walkAt(page.Layout, "/layout", 1, func(component Component, path string, _ int) {
		if component.Action != nil && component.Action.Type == ActionNavigate {
			if _, ok := pages[component.Action.Page]; !ok {
				issues = append(issues, ValidationIssue{Path: path + "/action/page", Code: "unknown_page", Message: fmt.Sprintf("page %q does not exist", component.Action.Page)})
			}
		}
	})
	if len(issues) > 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
}

func (v *Validator) validateSemantics(page Page) []ValidationIssue {
	var issues []ValidationIssue
	if page.Data != nil && (v.providers == nil || !v.providers.Has(page.Data.Provider)) {
		issues = append(issues, ValidationIssue{Path: "/data/provider", Code: "unknown_provider", Message: fmt.Sprintf("provider %q is not registered", page.Data.Provider)})
	}
	ids := make(map[string]string)
	count := 0
	walkAt(page.Layout, "/layout", 1, func(component Component, path string, depth int) {
		count++
		if depth > maxComponentDepth {
			issues = append(issues, ValidationIssue{Path: path, Code: "maximum_depth", Message: fmt.Sprintf("component nesting exceeds %d", maxComponentDepth)})
		}
		if component.ID != "" {
			if first, exists := ids[component.ID]; exists {
				issues = append(issues, ValidationIssue{Path: path + "/id", Code: "duplicate_id", Message: fmt.Sprintf("component ID %q is already used at %s", component.ID, first)})
			} else {
				ids[component.ID] = path
			}
		}
		issues = append(issues, validateComponent(component, path)...)
		if component.Action != nil && component.Action.Type == ActionInvoke && (v.actions == nil || !v.actions.Has(component.Action.Name)) {
			issues = append(issues, ValidationIssue{Path: path + "/action/name", Code: "unknown_action", Message: fmt.Sprintf("action %q is not registered", component.Action.Name)})
		}
	})
	if count > maxComponents {
		issues = append(issues, ValidationIssue{Path: "/layout", Code: "maximum_components", Message: fmt.Sprintf("page contains more than %d components", maxComponents)})
	}
	return issues
}

func validateComponent(component Component, path string) []ValidationIssue {
	var issues []ValidationIssue
	hasChildren := len(component.Children) > 0
	hasContent := component.Text != "" || component.Bind != "" || component.Label != "" || component.Style != "" || component.Format != "" || component.Suffix != "" || component.Action != nil
	problem := func(field, code, message string) {
		issues = append(issues, ValidationIssue{Path: path + field, Code: code, Message: message})
	}
	switch component.Type {
	case ComponentColumn, ComponentRow, ComponentCard:
		if !hasChildren {
			problem("/children", "required", "container must have at least one child")
		}
		if component.Text != "" || component.Bind != "" || component.Action != nil {
			problem("", "invalid_fields", "container cannot define text, bind, or action")
		}
	case ComponentText:
		if (component.Text == "") == (component.Bind == "") {
			problem("", "content_source", "text requires exactly one of text or bind")
		}
		if hasChildren || component.Action != nil {
			problem("", "invalid_fields", "text cannot have children or an action")
		}
	case ComponentMetric, ComponentProgress:
		if component.Bind == "" {
			problem("/bind", "required", string(component.Type)+" requires a binding")
		}
		if hasChildren || component.Text != "" || component.Action != nil {
			problem("", "invalid_fields", string(component.Type)+" cannot have children, text, or an action")
		}
	case ComponentDivider:
		if hasChildren || hasContent || component.ID != "" {
			problem("", "invalid_fields", "divider cannot define content, an ID, or children")
		}
	case ComponentList:
		if hasChildren == (component.Bind != "") {
			problem("", "content_source", "list requires exactly one of children or bind")
		}
		if component.Action != nil || component.Text != "" {
			problem("", "invalid_fields", "list cannot define text or an action")
		}
	case ComponentButton:
		if component.ID == "" {
			problem("/id", "required", "button requires an ID")
		}
		if component.Label == "" {
			problem("/label", "required", "button requires a label")
		}
		if component.Action == nil {
			problem("/action", "required", "button requires an action")
		}
		if hasChildren || component.Text != "" || component.Bind != "" {
			problem("", "invalid_fields", "button cannot define children, text, or bind")
		}
	default:
		problem("/type", "unknown_component", fmt.Sprintf("component type %q is not registered", component.Type))
	}
	if component.Bind != "" {
		if err := ValidateBinding(component.Bind); err != nil {
			problem("/bind", "invalid_binding", err.Error())
		}
	}
	return issues
}

func walkAt(component Component, path string, depth int, visit func(Component, string, int)) {
	visit(component, path, depth)
	for index, child := range component.Children {
		walkAt(child, path+"/children/"+strconv.Itoa(index), depth+1, visit)
	}
}

func schemaValidationError(err error) error {
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return &ValidationError{Issues: []ValidationIssue{{Path: "/", Code: "schema", Message: err.Error()}}}
	}
	var issues []ValidationIssue
	var flatten func(*jsonschema.ValidationError)
	flatten = func(item *jsonschema.ValidationError) {
		if len(item.Causes) > 0 {
			for _, cause := range item.Causes {
				flatten(cause)
			}
			return
		}
		path := "/" + strings.Join(item.InstanceLocation, "/")
		if path == "/" && len(item.InstanceLocation) == 0 {
			path = "/"
		}
		keyword := item.ErrorKind.KeywordPath()
		code := "schema"
		if len(keyword) > 0 {
			code = keyword[len(keyword)-1]
		}
		issues = append(issues, ValidationIssue{Path: path, Code: code, Message: item.Error()})
	}
	flatten(validation)
	return &ValidationError{Issues: issues}
}
