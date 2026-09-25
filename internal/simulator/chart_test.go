package simulator

import (
	"github.com/n0remac/Fabric/internal/fabric"
	"strings"
	"testing"
)

func TestChartRendersMonochromeLineFromStructuredSeries(t *testing.T) {
	page := fabric.Page{ID: "chart", Title: "Chart", Layout: fabric.Component{Type: fabric.ComponentChart, Bind: "series", X: "time", Y: "price"}}
	data := map[string]any{"series": []map[string]any{{"time": "2026-01-01T00:00:00Z", "price": 10.0}, {"time": "2026-01-02T00:00:00Z", "price": 12.0}}}
	node, err := NewGoDomRenderer().Render(page, data)
	if err != nil {
		t.Fatal(err)
	}
	output := node.Render()
	for _, part := range []string{"<svg", "<path", `stroke="currentColor"`, "Line chart with 2 samples"} {
		if !strings.Contains(output, part) {
			t.Fatalf("missing %q in %s", part, output)
		}
	}
}
