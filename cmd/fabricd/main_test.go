package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestServerURLs(t *testing.T) {
	var addresses []net.Addr
	for _, cidr := range []string{"127.0.0.1/8", "192.168.1.42/24", "100.101.102.103/32", "192.168.1.42/24", "fd00::42/64", "fe80::42/64", "::1/128"} {
		ip, network, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		network.IP = ip
		addresses = append(addresses, network)
	}
	for _, test := range []struct {
		address string
		want    []string
	}{
		{":8080", []string{"http://127.0.0.1:8080", "http://192.168.1.42:8080", "http://100.101.102.103:8080", "http://[fd00::42]:8080"}},
		{"[::]:9090", []string{"http://127.0.0.1:9090", "http://192.168.1.42:9090", "http://100.101.102.103:9090", "http://[fd00::42]:9090"}},
		{"0.0.0.0:8080", []string{"http://127.0.0.1:8080", "http://192.168.1.42:8080", "http://100.101.102.103:8080"}},
		{"127.0.0.1:8080", []string{"http://127.0.0.1:8080"}},
		{"192.168.1.42:9090", []string{"http://192.168.1.42:9090"}},
		{"[fd00::42]:8080", []string{"http://[fd00::42]:8080"}},
	} {
		t.Run(test.address, func(t *testing.T) {
			if got := serverURLs(test.address, addresses); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestWebsocketConfig(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("WEBSOCKET_ALLOWED_ORIGINS", "")
	config, err := websocketConfig(":9090")
	if err != nil {
		t.Fatal(err)
	}
	if !config.AllowMissingOrigin || len(config.AllowedOrigins) != 2 || config.AllowedOrigins[0] != "http://localhost:9090" {
		t.Fatalf("unexpected development config: %+v", config)
	}
	if err := config.ValidateRoomID("simulator-system"); err != nil {
		t.Fatal(err)
	}
	if err := config.ValidateRoomID("bad room"); err == nil {
		t.Fatal("expected invalid room to fail")
	}

	t.Setenv("ENVIRONMENT", "production")
	if _, err := websocketConfig(":9090"); err == nil {
		t.Fatal("production should require allowed origins")
	}
}

func TestSameOriginWebSocketSupportsRemoteHost(t *testing.T) {
	for _, test := range []struct {
		origin, host string
		want         bool
	}{
		{"http://100.101.102.103:8080", "100.101.102.103:8080", true},
		{"https://pi.tailnet.ts.net", "pi.tailnet.ts.net", true},
		{"http://localhost:8080", "localhost:8080", true},
		{"http://evil.example", "100.101.102.103:8080", false},
		{"null", "100.101.102.103:8080", false},
		{"http://100.101.102.103:8080/path", "100.101.102.103:8080", false},
	} {
		if got := sameOriginHost(test.origin, test.host); got != test.want {
			t.Errorf("origin=%q host=%q: got %v, want %v", test.origin, test.host, got, test.want)
		}
	}
	var forwarded string
	handler := allowSameOriginWebSocket(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = r.Header.Get("Origin")
		w.WriteHeader(http.StatusNoContent)
	}), "http://localhost:8080")
	request := httptest.NewRequest(http.MethodGet, "http://100.101.102.103:8080/ws/hub", nil)
	request.Header.Set("Origin", "http://100.101.102.103:8080")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if forwarded != "http://localhost:8080" {
		t.Fatalf("same-origin request was not admitted: %q", forwarded)
	}
	request.Header.Set("Origin", "http://evil.example")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if forwarded != "http://evil.example" {
		t.Fatalf("cross-origin request was rewritten: %q", forwarded)
	}
}
