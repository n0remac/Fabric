package simulator

import (
	"github.com/n0remac/Fabric/internal/fabric"
	"github.com/n0remac/Fabric/internal/pages"
	html "github.com/n0remac/GoDom/html"
)

func simulatorPage(page fabric.Page, summaries []pages.Summary, preview *html.Node) *html.Node {
	links := make([]*html.Node, 0, len(summaries))
	for _, summary := range summaries {
		class := "simulator-page-link"
		if summary.ID == page.ID {
			class += " simulator-page-link--active"
		}
		links = append(links, html.Li(
			html.A(html.Href("/simulator/"+summary.ID), html.Class(class), html.Text(summary.Title)),
		))
	}
	return html.Html(
		html.Attr("lang", "en"),
		html.Head(
			html.Meta(html.Charset("utf-8")),
			html.Meta(html.Attrs(map[string]string{"name": "viewport", "content": "width=device-width, initial-scale=1"})),
			html.Title(html.Text("Fabric Simulator — "+page.Title)),
			html.Link(html.Rel("stylesheet"), html.Href("/assets/fabric/fabric.css")),
			html.Script(html.Src("/assets/fabric/htmx.min.js")),
			html.Script(html.Src("/assets/fabric/ws.min.js")),
			html.Script(html.Src("/assets/fabric/simulator.js")),
		),
		html.Body(
			html.Div(
				html.Class("simulator-shell"),
				html.Attr("hx-ext", "ws"),
				html.Attr("ws-connect", "/ws/hub?room="+roomID(page.ID)),
				html.Header(html.Class("simulator-header"), html.H1(html.Text("Fabric Simulator"))),
				html.Div(
					html.Class("simulator-body"),
					html.Aside(
						html.Class("simulator-sidebar"),
						html.H2(html.Text("Pages")),
						html.Ul(html.Class("simulator-page-list"), html.Ch(links)),
					),
					html.Main(
						html.Class("simulator-main"),
						html.P(html.Class("simulator-size-label"), html.Text("800 × 480 X4 Preview")),
						html.Div(html.Class("simulator-viewport"), preview),
					),
				),
			),
		),
	)
}

func roomID(pageID string) string { return "simulator-" + pageID }
