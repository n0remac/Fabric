package simulator

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/n0remac/Fabric/internal/fabric"
	html "github.com/n0remac/GoDom/html"
)

type ComponentRenderer func(fabric.Page, fabric.Component, map[string]any) (*html.Node, error)

type GoDomRenderer struct {
	components map[fabric.ComponentType]ComponentRenderer
}

func NewGoDomRenderer() *GoDomRenderer {
	renderer := &GoDomRenderer{components: make(map[fabric.ComponentType]ComponentRenderer)}
	mustRegister := func(componentType fabric.ComponentType, render ComponentRenderer) {
		if err := renderer.Register(componentType, render); err != nil {
			panic(err)
		}
	}
	mustRegister(fabric.ComponentColumn, renderer.renderContainer("fabric-column", html.Div))
	mustRegister(fabric.ComponentRow, renderer.renderContainer("fabric-row", html.Div))
	mustRegister(fabric.ComponentCard, renderer.renderContainer("fabric-card", html.Section))
	mustRegister(fabric.ComponentText, renderer.renderText)
	mustRegister(fabric.ComponentMetric, renderer.renderMetric)
	mustRegister(fabric.ComponentDivider, renderer.renderDivider)
	mustRegister(fabric.ComponentList, renderer.renderList)
	mustRegister(fabric.ComponentProgress, renderer.renderProgress)
	mustRegister(fabric.ComponentButton, renderer.renderButton)
	mustRegister(fabric.ComponentChart, renderer.renderChart)
	return renderer
}

// Charts accept an array of objects. X is a number or RFC3339 timestamp; Y is
// numeric. Coordinates are derived from those values rather than raster images.
func (r *GoDomRenderer) renderChart(_ fabric.Page, component fabric.Component, data map[string]any) (*html.Node, error) {
	value, err := fabric.ResolveBinding(data, component.Bind)
	type sample struct{ x, y float64 }
	var values []sample
	if err == nil {
		series := reflect.ValueOf(value)
		if series.IsValid() && (series.Kind() == reflect.Slice || series.Kind() == reflect.Array) {
			for i := 0; i < series.Len(); i++ {
				item := series.Index(i).Interface()
				x, validX := chartX(chartField(item, component.X))
				y, validY := fabric.Number(chartField(item, component.Y))
				if validX && validY {
					values = append(values, sample{x, y})
				}
			}
		}
	}
	if len(values) == 0 {
		return html.Div(html.Class("fabric-chart fabric-chart--empty"), html.Text("Chart unavailable")), nil
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].x < values[j].x })
	minX, maxX, minY, maxY := values[0].x, values[len(values)-1].x, values[0].y, values[0].y
	for _, point := range values {
		if point.y < minY {
			minY = point.y
		}
		if point.y > maxY {
			maxY = point.y
		}
	}
	spanX, spanY := maxX-minX, maxY-minY
	var path strings.Builder
	for i, point := range values {
		x := 320.0
		if spanX > 0 {
			x = 8 + (point.x-minX)/spanX*624
		}
		y := 90.0
		if spanY > 0 {
			y = 172 - (point.y-minY)/spanY*164
		}
		if i == 0 {
			path.WriteByte('M')
		} else {
			path.WriteByte('L')
		}
		path.WriteString(fmt.Sprintf("%.1f %.1f", x, y))
	}
	return html.Div(html.Class("fabric-chart"), html.Svg(
		html.Attr("viewBox", "0 0 640 180"),
		html.Attr("role", "img"),
		html.Attr("aria-label", fmt.Sprintf("Line chart with %d samples", len(values))),
		html.Path(html.Attr("d", path.String()), html.Attr("fill", "none"), html.Attr("stroke", "currentColor"), html.Attr("stroke-width", "3")),
	)), nil
}

func chartX(value any) (float64, bool) {
	if numeric, ok := fabric.Number(value); ok {
		return numeric, true
	}
	if timestamp, ok := value.(time.Time); ok {
		return float64(timestamp.Unix()), !timestamp.IsZero()
	}
	if text, ok := value.(string); ok {
		if timestamp, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return float64(timestamp.UnixNano()) / 1e9, true
		}
	}
	return 0, false
}

func chartField(item any, field string) any {
	value := reflect.ValueOf(item)
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Map {
		if value.Type().Key().Kind() != reflect.String {
			return nil
		}
		entry := value.MapIndex(reflect.ValueOf(field))
		if entry.IsValid() {
			return entry.Interface()
		}
	}
	if value.Kind() == reflect.Struct {
		for i := 0; i < value.NumField(); i++ {
			name := strings.Split(value.Type().Field(i).Tag.Get("json"), ",")[0]
			if name == field && value.Field(i).CanInterface() {
				return value.Field(i).Interface()
			}
		}
	}
	return nil
}

func (r *GoDomRenderer) Register(componentType fabric.ComponentType, render ComponentRenderer) error {
	if componentType == "" {
		return errors.New("component type is required")
	}
	if render == nil {
		return errors.New("component renderer is required")
	}
	if _, exists := r.components[componentType]; exists {
		return fmt.Errorf("component renderer %q is already registered", componentType)
	}
	r.components[componentType] = render
	return nil
}

func (r *GoDomRenderer) Render(page fabric.Page, data map[string]any) (*html.Node, error) {
	content, err := r.renderComponent(page, page.Layout, data)
	if err != nil {
		return nil, err
	}
	options := []*html.Node{
		html.Id("fabric-display"),
		html.Class("fabric-display"),
		html.Attr("aria-label", page.Title+" page preview"),
		content,
	}
	if page.Data != nil && page.Data.Refresh.Strategy == fabric.RefreshInterval {
		options = append(options,
			html.HxGet("/simulator/"+page.ID+"/render"),
			html.HxTrigger(fmt.Sprintf("every %ds", page.Data.Refresh.Seconds)),
			html.HxTarget("this"),
			html.HxSwap("outerHTML"),
		)
	}
	return html.Div(options...), nil
}

func (r *GoDomRenderer) renderComponent(page fabric.Page, component fabric.Component, data map[string]any) (*html.Node, error) {
	render, ok := r.components[component.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported component type %q", component.Type)
	}
	return render(page, component, data)
}

func (r *GoDomRenderer) renderContainer(class string, element func(...*html.Node) *html.Node) ComponentRenderer {
	return func(page fabric.Page, component fabric.Component, data map[string]any) (*html.Node, error) {
		children := make([]*html.Node, 0, len(component.Children)+2)
		children = append(children, html.Class(class))
		if component.ID != "" {
			children = append(children, html.Id(component.ID))
		}
		for _, child := range component.Children {
			node, err := r.renderComponent(page, child, data)
			if err != nil {
				return nil, err
			}
			children = append(children, node)
		}
		return element(children...), nil
	}
}

func (r *GoDomRenderer) renderText(_ fabric.Page, component fabric.Component, data map[string]any) (*html.Node, error) {
	text := component.Text
	if component.Bind != "" {
		text = fabric.BoundDisplay(data, component)
	}
	class := "fabric-text"
	if component.Style != "" {
		class += " fabric-text--" + string(component.Style)
	}
	options := []*html.Node{html.Class(class), html.Text(text)}
	if component.ID != "" {
		options = append([]*html.Node{html.Id(component.ID)}, options...)
	}
	if component.Style == fabric.StyleHeading {
		return html.H1(options...), nil
	}
	if component.Style == fabric.StyleSubheading {
		return html.H2(options...), nil
	}
	return html.P(options...), nil
}

func (r *GoDomRenderer) renderMetric(_ fabric.Page, component fabric.Component, data map[string]any) (*html.Node, error) {
	return html.Div(
		html.Class("fabric-metric"),
		html.Span(html.Class("fabric-metric__label"), html.Text(component.Label)),
		html.Span(html.Class("fabric-metric__value"), html.Text(fabric.BoundDisplay(data, component))),
	), nil
}

func (r *GoDomRenderer) renderDivider(_ fabric.Page, _ fabric.Component, _ map[string]any) (*html.Node, error) {
	return html.Hr(html.Class("fabric-divider")), nil
}

func (r *GoDomRenderer) renderList(page fabric.Page, component fabric.Component, data map[string]any) (*html.Node, error) {
	items := make([]*html.Node, 0)
	if component.Bind != "" {
		value, err := fabric.ResolveBinding(data, component.Bind)
		if err != nil {
			items = append(items, html.Li(html.Text("—")))
		} else {
			reflected := reflect.ValueOf(value)
			if reflected.IsValid() && (reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array) {
				for index := 0; index < reflected.Len(); index++ {
					items = append(items, html.Li(html.Text(fmt.Sprint(reflected.Index(index).Interface()))))
				}
			} else {
				items = append(items, html.Li(html.Text("—")))
			}
		}
	} else {
		for _, child := range component.Children {
			node, err := r.renderComponent(page, child, data)
			if err != nil {
				return nil, err
			}
			items = append(items, html.Li(node))
		}
	}
	options := []*html.Node{html.Class("fabric-list"), html.Ch(items)}
	if component.ID != "" {
		options = append([]*html.Node{html.Id(component.ID)}, options...)
	}
	return html.Ul(options...), nil
}

func (r *GoDomRenderer) renderProgress(_ fabric.Page, component fabric.Component, data map[string]any) (*html.Node, error) {
	display := fabric.BoundDisplay(data, component)
	value := 0.0
	if bound, err := fabric.ResolveBinding(data, component.Bind); err == nil {
		if number, ok := fabric.Number(bound); ok {
			value = math.Max(0, math.Min(100, number))
		}
	}
	return html.Div(
		html.Class("fabric-progress"),
		html.Div(
			html.Class("fabric-progress__header"),
			html.Span(html.Text(component.Label)),
			html.Span(html.Text(display)),
		),
		html.Progress(
			html.Class("fabric-progress__bar"),
			html.Max("100"),
			html.Value(strconv.FormatFloat(value, 'f', -1, 64)),
			html.Text(display),
		),
	), nil
}

func (r *GoDomRenderer) renderButton(page fabric.Page, component fabric.Component, _ map[string]any) (*html.Node, error) {
	actionURL := "/simulator/" + page.ID + "/actions"
	options := []*html.Node{
		html.Class("fabric-action"),
		html.Method("POST"),
		html.Action(actionURL),
		html.HxPost(actionURL),
		html.HxTarget("#fabric-display"),
		html.HxSwap("outerHTML"),
		html.Input(html.Type("hidden"), html.Name("component_id"), html.Value(component.ID)),
		html.Button(html.Type("submit"), html.Class("fabric-button"), html.Text(component.Label)),
	}
	if component.Action != nil {
		options = append(options, html.Attr("data-fabric-action", string(component.Action.Type)))
	}
	return html.Form(options...), nil
}
