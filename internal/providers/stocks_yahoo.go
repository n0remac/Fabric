package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// YahooChartSource isolates the upstream chart response from Fabric's data contract.
// Yahoo's chart endpoint is public but undocumented, so callers should tolerate outages.
type YahooChartSource struct {
	Client  *http.Client
	BaseURL string
}

func NewYahooChartSource() *YahooChartSource {
	return &YahooChartSource{Client: &http.Client{Timeout: 8 * time.Second}, BaseURL: "https://query1.finance.yahoo.com"}
}

type yahooEnvelope struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Symbol             string  `json:"symbol"`
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				PreviousClose      float64 `json:"previousClose"`
				ChartPreviousClose float64 `json:"chartPreviousClose"`
				RegularMarketTime  int64   `json:"regularMarketTime"`
				MarketState        string  `json:"marketState"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Close  []*float64 `json:"close"`
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Volume []*int64   `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error any `json:"error"`
	} `json:"chart"`
}

func (s *YahooChartSource) fetch(ctx context.Context, symbol, period, interval string) (yahooEnvelope, error) {
	if !symbolPattern.MatchString(symbol) {
		return yahooEnvelope{}, errors.New("invalid ticker")
	}
	base := s.BaseURL
	if base == "" {
		base = "https://query1.finance.yahoo.com"
	}
	endpoint, err := url.Parse(base)
	if err != nil {
		return yahooEnvelope{}, err
	}
	endpoint.Path = "/v8/finance/chart/" + symbol
	query := endpoint.Query()
	query.Set("range", period)
	query.Set("interval", interval)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return yahooEnvelope{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return yahooEnvelope{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return yahooEnvelope{}, fmt.Errorf("market data HTTP %d", resp.StatusCode)
	}
	var body yahooEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return yahooEnvelope{}, err
	}
	if body.Chart.Error != nil || len(body.Chart.Result) == 0 {
		return yahooEnvelope{}, errors.New("market data unavailable")
	}
	return body, nil
}

func (s *YahooChartSource) Quotes(ctx context.Context, symbols []string) ([]Quote, error) {
	quotes := make([]Quote, 0, len(symbols))
	for _, symbol := range symbols {
		body, err := s.fetch(ctx, symbol, "1d", "5m")
		if err != nil {
			return nil, err
		}
		result := body.Chart.Result[0]
		meta := result.Meta
		previous := meta.PreviousClose
		if previous <= 0 {
			previous = meta.ChartPreviousClose
		}
		quote := Quote{Symbol: symbol, Price: meta.RegularMarketPrice, PreviousClose: previous, Session: normalizeMarketState(meta.MarketState)}
		if meta.RegularMarketTime > 0 {
			quote.Time = time.Unix(meta.RegularMarketTime, 0)
		}
		if len(result.Indicators.Quote) > 0 {
			data := result.Indicators.Quote[0]
			for _, value := range data.Open {
				if value != nil {
					quote.Open = *value
					break
				}
			}
			for _, value := range data.High {
				if value != nil && *value > quote.High {
					quote.High = *value
				}
			}
			for _, value := range data.Low {
				if value != nil && (quote.Low == 0 || *value < quote.Low) {
					quote.Low = *value
				}
			}
			for _, value := range data.Volume {
				if value != nil {
					quote.Volume += *value
				}
			}
		}
		if !finitePositive(quote.Price) {
			return nil, fmt.Errorf("missing price for %s", symbol)
		}
		quotes = append(quotes, quote)
	}
	return quotes, nil
}

func normalizeMarketState(state string) string {
	switch state {
	case "REGULAR":
		return "OPEN"
	case "CLOSED":
		return "CLOSED"
	case "PRE":
		return "PRE-MARKET"
	case "POST":
		return "AFTER HOURS"
	default:
		return ""
	}
}

func (s *YahooChartSource) History(ctx context.Context, symbol string, period Period) ([]PricePoint, error) {
	var upstreamPeriod, interval string
	switch period {
	case Period1D:
		upstreamPeriod, interval = "1d", "5m"
	case Period5D:
		upstreamPeriod, interval = "5d", "30m"
	case Period1M:
		upstreamPeriod, interval = "1mo", "1d"
	case Period1Y:
		upstreamPeriod, interval = "1y", "1wk"
	default:
		return nil, fmt.Errorf("invalid chart period %q", period)
	}
	body, err := s.fetch(ctx, symbol, upstreamPeriod, interval)
	if err != nil {
		return nil, err
	}
	result := body.Chart.Result[0]
	if len(result.Indicators.Quote) == 0 {
		return nil, errors.New("market history unavailable")
	}
	closes := result.Indicators.Quote[0].Close
	points := make([]PricePoint, 0, len(result.Timestamp))
	for index, timestamp := range result.Timestamp {
		if index < len(closes) && closes[index] != nil && finitePositive(*closes[index]) {
			points = append(points, PricePoint{Time: time.Unix(timestamp, 0), Price: *closes[index]})
		}
	}
	if len(points) == 0 {
		return nil, errors.New("market history unavailable")
	}
	return points, nil
}
