package providers

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type stockRoundTrip func(*http.Request) (*http.Response, error)

func (f stockRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestYahooSourceNormalizesQuoteAndHistory(t *testing.T) {
	client := &http.Client{Transport: stockRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("User-Agent") != "Mozilla/5.0" {
			t.Errorf("unexpected user agent %q", r.Header.Get("User-Agent"))
		}
		if r.URL.Path != "/v8/finance/chart/AAPL" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("range") == "1y" && r.URL.Query().Get("interval") != "1wk" {
			t.Errorf("unexpected yearly interval: %s", r.URL.RawQuery)
		}
		body := `{"chart":{"result":[{"meta":{"symbol":"AAPL","regularMarketPrice":252.18,"previousClose":248.84,"regularMarketTime":1790362920,"marketState":"REGULAR"},"timestamp":[1790353800,1790354100],"indicators":{"quote":[{"open":[249.31,250],"high":[251,253.02],"low":[248.77,249],"volume":[20000000,22100000],"close":[249.31,252.18]}]}}],"error":null}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	source := &YahooChartSource{Client: client, BaseURL: "https://example.test"}
	quotes, err := source.Quotes(t.Context(), []string{"AAPL"})
	if err != nil {
		t.Fatal(err)
	}
	if len(quotes) != 1 || quotes[0].Session != "OPEN" || quotes[0].Volume != 42100000 || quotes[0].High != 253.02 {
		t.Fatalf("unexpected quote: %+v", quotes)
	}
	points, err := source.History(t.Context(), "AAPL", Period1Y)
	if err != nil || len(points) != 2 || points[1].Price != 252.18 {
		t.Fatalf("points=%+v err=%v", points, err)
	}
}
