package fabric

import (
	"errors"
	"testing"
)

func TestResolveBindingAndFormatting(t *testing.T) {
	data := map[string]any{"system": map[string]any{"cpu": 18, "uptime": 100800}}
	value, err := ResolveBinding(data, "system.cpu")
	if err != nil || value != 18 {
		t.Fatalf("ResolveBinding: value=%v err=%v", value, err)
	}
	if got := BoundDisplay(data, Component{Bind: "system.cpu", Suffix: "%"}); got != "18%" {
		t.Fatalf("expected 18%%, got %q", got)
	}
	if got := BoundDisplay(data, Component{Bind: "system.uptime", Format: FormatDuration}); got != "1d 4h" {
		t.Fatalf("expected duration, got %q", got)
	}
	if got := BoundDisplay(data, Component{Bind: "system.missing", Suffix: "%"}); got != "—" {
		t.Fatalf("missing binding should be em dash, got %q", got)
	}
}

func TestResolveBindingErrors(t *testing.T) {
	data := map[string]any{"system": 1}
	if _, err := ResolveBinding(data, "system.cpu"); !errors.Is(err, ErrBindingObject) {
		t.Fatalf("expected ErrBindingObject, got %v", err)
	}
	if _, err := ResolveBinding(data, "system..cpu"); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("expected ErrInvalidBinding, got %v", err)
	}
}
