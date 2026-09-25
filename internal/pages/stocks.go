package pages

import (
	"errors"
	"github.com/n0remac/Fabric/internal/fabric"
)

// StockWatchlistTransform expands configured ticker controls into declared page
// components. The stored JSON stays reusable and clients receive a normal page.
func StockWatchlistTransform(symbols []string) func(fabric.Page) (fabric.Page, error) {
	return func(page fabric.Page) (fabric.Page, error) {
		if page.ID != "stocks" {
			return page, nil
		}
		if len(page.Layout.Children) < 2 {
			return page, errors.New("stocks page needs heading and divider")
		}
		children := append([]fabric.Component(nil), page.Layout.Children[:2]...)
		for _, symbol := range symbols {
			prefix := "stocks.items." + symbol
			children = append(children, fabric.Component{Type: fabric.ComponentRow, ID: "stock_row_" + symbol, Children: []fabric.Component{
				{Type: fabric.ComponentButton, ID: "stock_" + symbol, Label: symbol, Action: &fabric.Action{Type: fabric.ActionInvoke, Name: "stocks.select", Args: map[string]any{"symbol": symbol}}},
				{Type: fabric.ComponentMetric, Label: "Price", Bind: prefix + ".price", Format: fabric.FormatCurrency},
				{Type: fabric.ComponentMetric, Label: "Change", Bind: prefix + ".change", Format: fabric.FormatSigned},
				{Type: fabric.ComponentMetric, Label: "Daily", Bind: prefix + ".change_percent", Format: fabric.FormatPercent},
			}})
		}
		page.Layout.Children = append(children, page.Layout.Children[2:]...)
		return page, nil
	}
}
