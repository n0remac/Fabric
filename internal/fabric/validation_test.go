package fabric

import (
	"errors"
	"testing"
)

type names map[string]bool

func (n names) Has(name string) bool { return n[name] }

func TestValidatorAcceptsPageAndRejectsUnknownFields(t *testing.T) {
	validator, err := NewValidator(names{}, names{"system": true})
	if err != nil {
		t.Fatal(err)
	}
	valid := []byte(`{
      "fabric":"0.1","id":"system","title":"System",
      "layout":{"type":"column","children":[
        {"type":"text","text":"Pi","style":"heading"},
        {"type":"button","id":"refresh","label":"Refresh","action":{"type":"refresh"}}
      ]},
      "data":{"provider":"system","refresh":{"strategy":"manual"}}
    }`)
	page, err := validator.Decode(valid)
	if err != nil || page.ID != "system" {
		t.Fatalf("Decode valid: page=%+v err=%v", page, err)
	}

	invalid := []byte(`{"fabric":"0.1","id":"system","title":"System","extra":true,"layout":{"type":"text","text":"Pi"}}`)
	_, err = validator.Decode(invalid)
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Issues) == 0 {
		t.Fatalf("expected field-specific validation error, got %v", err)
	}
}

func TestValidatorSemanticErrors(t *testing.T) {
	validator, err := NewValidator(names{}, names{"system": true})
	if err != nil {
		t.Fatal(err)
	}
	document := []byte(`{
      "fabric":"0.1","id":"system","title":"System",
      "layout":{"type":"column","children":[
        {"type":"button","id":"same","label":"One","action":{"type":"invoke","name":"missing"}},
        {"type":"button","id":"same","label":"Two","action":{"type":"refresh"}}
      ]}
    }`)
	_, err = validator.Decode(document)
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	codes := make(map[string]bool)
	for _, issue := range validation.Issues {
		codes[issue.Code] = true
	}
	if !codes["duplicate_id"] || !codes["unknown_action"] {
		t.Fatalf("expected duplicate and action errors, got %+v", validation.Issues)
	}
}
