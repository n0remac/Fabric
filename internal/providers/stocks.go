package providers

import (
	"context"
	"errors"
	"fmt"
	"github.com/n0remac/Fabric/internal/nodes"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Period string

const (
	Period1D Period = "1d"
	Period5D Period = "5d"
	Period1M Period = "1m"
	Period1Y Period = "1y"
)

func ValidPeriod(period Period) bool {
	switch period {
	case Period1D, Period5D, Period1M, Period1Y:
		return true
	}
	return false
}

type Quote struct {
	Symbol                                string
	Price, PreviousClose, Open, High, Low float64
	Volume                                int64
	Time                                  time.Time
	Session                               string
}

type PricePoint struct {
	Time  time.Time `json:"time"`
	Price float64   `json:"price"`
}

type MarketDataSource interface {
	Quotes(context.Context, []string) ([]Quote, error)
	History(context.Context, string, Period) ([]PricePoint, error)
}

var symbolPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,15}$`)

func ParseStockTickers(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		value = "AAPL,NVDA,GOOG,MSFT,VOO"
	}
	var symbols []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(value, ",") {
		symbol := strings.ToUpper(strings.TrimSpace(raw))
		if !symbolPattern.MatchString(symbol) {
			return nil, fmt.Errorf("invalid stock ticker %q", raw)
		}
		if !seen[symbol] {
			seen[symbol] = true
			symbols = append(symbols, symbol)
		}
	}
	if len(symbols) > 7 {
		return nil, errors.New("stock watchlist cannot exceed 7 tickers on the 800×480 page")
	}
	return symbols, nil
}

type historyCache struct {
	points    []PricePoint
	fetched   time.Time
	attempted time.Time
	stale     bool
}

type StockProvider struct {
	mu             sync.Mutex
	source         MarketDataSource
	symbols        []string
	allowed        map[string]bool
	state          *nodes.State
	quotes         map[string]Quote
	quoteFetched   time.Time
	quoteAttempted time.Time
	quoteStale     bool
	history        map[string]historyCache
	now            func() time.Time
	changes        chan string
}

func NewStockProvider(source MarketDataSource, symbols []string) (*StockProvider, error) {
	if source == nil {
		return nil, errors.New("market data source is required")
	}
	if len(symbols) == 0 {
		return nil, errors.New("stock watchlist is empty")
	}
	allowed := map[string]bool{}
	for _, symbol := range symbols {
		if !symbolPattern.MatchString(symbol) || allowed[symbol] {
			return nil, fmt.Errorf("invalid or duplicate stock ticker %q", symbol)
		}
		allowed[symbol] = true
	}
	return &StockProvider{source: source, symbols: append([]string(nil), symbols...), allowed: allowed, state: nodes.NewState(), history: map[string]historyCache{}, now: time.Now, changes: make(chan string, 1)}, nil
}

type selection struct {
	symbol string
	period Period
}

func (p *StockProvider) selection(ctx context.Context) selection {
	id := "local"
	if n, ok := nodes.FromContext(ctx); ok {
		id = n.NodeID
	}
	if value, ok := p.state.Get(id, "stocks", "selection"); ok {
		return value.(selection)
	}
	return selection{symbol: p.symbols[0], period: Period1D}
}
func (p *StockProvider) setSelection(ctx context.Context, value selection) {
	id := "local"
	if n, ok := nodes.FromContext(ctx); ok {
		id = n.NodeID
	}
	p.state.Set(id, "stocks", "selection", value)
}

func (p *StockProvider) Name() string           { return "stocks" }
func (p *StockProvider) NodeScoped() bool       { return true }
func (p *StockProvider) Symbols() []string      { return append([]string(nil), p.symbols...) }
func (p *StockProvider) Changes() <-chan string { return p.changes }

func (p *StockProvider) signal() {
	select {
	case p.changes <- "stock-detail":
	default:
	}
}

func (p *StockProvider) Select(symbol string) error { return p.SelectFor(context.Background(), symbol) }
func (p *StockProvider) SelectFor(ctx context.Context, symbol string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.allowed[symbol] {
		return fmt.Errorf("ticker %q is not in the watchlist", symbol)
	}
	p.setSelection(ctx, selection{symbol: symbol, period: Period1D})
	p.signal()
	return nil
}

func (p *StockProvider) SetPeriod(period Period) error {
	return p.SetPeriodFor(context.Background(), period)
}
func (p *StockProvider) SetPeriodFor(ctx context.Context, period Period) error {
	if !ValidPeriod(period) {
		return fmt.Errorf("invalid chart period %q", period)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.selection(ctx)
	current.period = period
	p.setSelection(ctx, current)
	p.signal()
	return nil
}

func (p *StockProvider) Invalidate() { p.InvalidateFor(context.Background()) }
func (p *StockProvider) InvalidateFor(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.quoteAttempted = time.Time{}
	current := p.selection(ctx)
	key := current.symbol + ":" + string(current.period)
	entry := p.history[key]
	entry.attempted = time.Time{}
	p.history[key] = entry
}

func (p *StockProvider) Data(ctx context.Context) (map[string]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.selection(ctx)
	now := p.now()
	if p.quoteAttempted.IsZero() || now.Sub(p.quoteAttempted) >= 45*time.Second {
		p.quoteAttempted = now
		quotes, err := p.source.Quotes(ctx, p.symbols)
		if err == nil {
			mapped := make(map[string]Quote, len(quotes))
			for _, quote := range quotes {
				if p.allowed[quote.Symbol] && finitePositive(quote.Price) {
					mapped[quote.Symbol] = quote
				}
			}
			if len(mapped) == len(p.symbols) {
				p.quotes, p.quoteFetched, p.quoteStale = mapped, now, false
			} else {
				err = errors.New("incomplete quote response")
			}
		}
		if err != nil {
			p.quoteStale = true
		}
	}
	status := "fresh"
	if len(p.quotes) == 0 {
		status = "unavailable"
	} else if p.quoteStale {
		status = "cached"
	}
	items := map[string]any{}
	for _, symbol := range p.symbols {
		if quote, ok := p.quotes[symbol]; ok {
			items[symbol] = quoteData(quote)
		}
	}
	selected := map[string]any{"symbol": current.symbol, "period": string(current.period), "history": []PricePoint{}, "status": status, "stale": status != "fresh"}
	selected["period_label"] = strings.ToUpper(string(current.period))
	if quote, ok := p.quotes[current.symbol]; ok {
		for key, value := range quoteData(quote) {
			selected[key] = value
		}
	}
	key := current.symbol + ":" + string(current.period)
	entry := p.history[key]
	ttl := 3 * time.Minute
	if current.period == Period1M || current.period == Period1Y {
		ttl = 30 * time.Minute
	}
	if entry.attempted.IsZero() || now.Sub(entry.attempted) >= ttl {
		entry.attempted = now
		points, err := p.source.History(ctx, current.symbol, current.period)
		if err == nil && len(points) > 0 {
			valid := make([]PricePoint, 0, len(points))
			for _, point := range points {
				if finitePositive(point.Price) && !point.Time.IsZero() {
					valid = append(valid, point)
				}
			}
			if len(valid) > 0 {
				entry = historyCache{points: valid, fetched: now, attempted: now}
			} else {
				entry.stale = true
			}
		} else {
			entry.stale = true
		}
		p.history[key] = entry
	}
	if len(entry.points) > 0 {
		selected["history"] = append([]PricePoint(nil), entry.points...)
	}
	if entry.stale {
		selected["stale"] = true
		selected["status"] = "cached"
		if len(entry.points) == 0 {
			selected["status"] = "unavailable"
		}
	}
	updated := ""
	if !p.quoteFetched.IsZero() {
		updated = p.quoteFetched.Format(time.RFC3339)
	}
	displayStatus := "Market data unavailable"
	if status == "fresh" {
		displayStatus = "Updated " + p.quoteFetched.Format("3:04 PM")
	}
	if status == "cached" {
		displayStatus = "Data delayed · updated " + p.quoteFetched.Format("3:04 PM")
	}
	if entry.stale && status == "fresh" {
		displayStatus = "Chart data delayed · updated " + p.quoteFetched.Format("3:04 PM")
	}
	return map[string]any{"stocks": map[string]any{"updated_at": updated, "status": status, "stale": status != "fresh", "display_status": displayStatus, "items": items, "selected": selected}}, nil
}

func quoteData(quote Quote) map[string]any {
	change := quote.Price - quote.PreviousClose
	percent := 0.0
	if quote.PreviousClose > 0 {
		percent = change / quote.PreviousClose * 100
	}
	timestamp := ""
	if !quote.Time.IsZero() {
		timestamp = quote.Time.Format(time.RFC3339)
	}
	return map[string]any{"symbol": quote.Symbol, "price": quote.Price, "change": change, "change_percent": percent, "open": quote.Open, "high": quote.High, "low": quote.Low, "previous_close": quote.PreviousClose, "volume": quote.Volume, "market_timestamp": timestamp, "session": quote.Session}
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
