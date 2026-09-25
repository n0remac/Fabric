package providers

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeMarket struct {
	quotes                   []Quote
	points                   []PricePoint
	quoteErr, historyErr     error
	quoteCalls, historyCalls int
}

func (f *fakeMarket) Quotes(_ context.Context, _ []string) ([]Quote, error) {
	f.quoteCalls++
	return f.quotes, f.quoteErr
}
func (f *fakeMarket) History(_ context.Context, _ string, _ Period) ([]PricePoint, error) {
	f.historyCalls++
	return f.points, f.historyErr
}

func stockPayload(t *testing.T, p *StockProvider) map[string]any {
	t.Helper()
	data, err := p.Data(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return data["stocks"].(map[string]any)
}

func TestStockProviderNormalizesCachesAndServesStaleData(t *testing.T) {
	now := time.Date(2026, 9, 25, 14, 42, 0, 0, time.FixedZone("PDT", -7*3600))
	fake := &fakeMarket{
		quotes: []Quote{{Symbol: "AAPL", Price: 252.18, PreviousClose: 248.84, Open: 249.31, High: 253.02, Low: 248.77, Volume: 42100000, Time: now}},
		points: []PricePoint{{Time: now.Add(-time.Hour), Price: 249.31}, {Time: now, Price: 252.18}},
	}
	provider, err := NewStockProvider(fake, []string{"AAPL"})
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return now }
	data := stockPayload(t, provider)
	selected := data["selected"].(map[string]any)
	if selected["change"].(float64) < 3.33 || selected["change_percent"].(float64) < 1.34 || selected["volume"] != int64(42100000) {
		t.Fatalf("incorrect normalization: %+v", selected)
	}
	stockPayload(t, provider)
	if fake.quoteCalls != 1 || fake.historyCalls != 1 {
		t.Fatalf("cache missed: quotes=%d history=%d", fake.quoteCalls, fake.historyCalls)
	}
	now = now.Add(46 * time.Second)
	fake.quoteErr = errors.New("upstream down")
	data = stockPayload(t, provider)
	if data["status"] != "cached" || data["stale"] != true || data["items"].(map[string]any)["AAPL"] == nil {
		t.Fatalf("cached quotes lost on failure: %+v", data)
	}
	stockPayload(t, provider)
	if fake.quoteCalls != 2 {
		t.Fatalf("failure should be throttled, calls=%d", fake.quoteCalls)
	}
	now = now.Add(4 * time.Minute)
	fake.historyErr = errors.New("history down")
	data = stockPayload(t, provider)
	selected = data["selected"].(map[string]any)
	if selected["status"] != "cached" || len(selected["history"].([]PricePoint)) != 2 {
		t.Fatalf("history cache lost: %+v", selected)
	}
	stockPayload(t, provider)
	if fake.historyCalls != 2 {
		t.Fatalf("history failure should be throttled, calls=%d", fake.historyCalls)
	}
}

func TestStockProviderAllowlistAndPeriod(t *testing.T) {
	fake := &fakeMarket{}
	provider, err := NewStockProvider(fake, []string{"AAPL", "NVDA"})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Select("EVIL"); err == nil {
		t.Fatal("accepted unconfigured ticker")
	}
	if err := provider.SetPeriod("2y"); err == nil {
		t.Fatal("accepted unsupported period")
	}
	if err := provider.Select("NVDA"); err != nil {
		t.Fatal(err)
	}
	if err := provider.SetPeriod(Period1Y); err != nil {
		t.Fatal(err)
	}
	data := stockPayload(t, provider)
	selected := data["selected"].(map[string]any)
	if selected["symbol"] != "NVDA" || selected["period"] != "1y" {
		t.Fatalf("selection not applied: %+v", selected)
	}
	if data["status"] != "unavailable" {
		t.Fatalf("unexpected status: %+v", data)
	}
	if _, err := ParseStockTickers("AAPL,broken.ticker"); err == nil {
		t.Fatal("accepted non-bindable ticker")
	}
}
