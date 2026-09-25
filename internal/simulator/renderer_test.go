package simulator

import (
	"strings"
	"testing"

	"github.com/n0remac/Fabric/internal/fabric"
	"github.com/n0remac/Fabric/internal/pages"
)

func TestRendererEscapesContentAndRendersComponents(t *testing.T) {
	page := fabric.Page{
		ID: "system", Title: "System",
		Data: &fabric.DataSpec{Refresh: fabric.RefreshSpec{Strategy: fabric.RefreshInterval, Seconds: 60}},
		Layout: fabric.Component{Type: fabric.ComponentColumn, Children: []fabric.Component{
			{Type: fabric.ComponentText, Text: `<script>alert("x")</script>`, Style: fabric.StyleHeading},
			{Type: fabric.ComponentMetric, Label: "CPU", Bind: "system.cpu", Suffix: "%"},
			{Type: fabric.ComponentProgress, Label: "Memory", Bind: "system.memory", Suffix: "%"},
			{Type: fabric.ComponentList, Bind: "items"},
			{Type: fabric.ComponentDivider},
			{Type: fabric.ComponentButton, ID: "refresh", Label: "Refresh", Action: &fabric.Action{Type: fabric.ActionRefresh}},
		}},
	}
	node, err := NewGoDomRenderer().Render(page, map[string]any{
		"system": map[string]any{"cpu": 18, "memory": 42},
		"items":  []string{"one", "two"},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := node.Render()
	for _, expected := range []string{"&lt;script&gt;", "18%", "42%", "<progress", "<ul", `/simulator/system/actions`, `hx-trigger="every 60s"`} {
		if !strings.Contains(output, expected) {
			t.Errorf("rendered output missing %q: %s", expected, output)
		}
	}
	if strings.Contains(output, `<script>alert`) {
		t.Fatal("page text was rendered as raw HTML")
	}
}

func TestSimulatorLayoutUsesOnlyLocalAssetsAndFixedViewport(t *testing.T) {
	page := fabric.Page{ID: "system", Title: "System"}
	output := simulatorPage(page, []pages.Summary{{ID: "system", Title: "System"}}, nil).Render()
	if strings.Contains(output, "https://") || strings.Contains(output, "http://") {
		t.Fatalf("layout contains external asset URL: %s", output)
	}
	for _, expected := range []string{"/assets/fabric/htmx.min.js", "/assets/fabric/ws.min.js", "simulator-system", "800 × 480"} {
		if !strings.Contains(output, expected) {
			t.Errorf("layout missing %q", expected)
		}
	}
}
