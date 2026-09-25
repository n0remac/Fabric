package providers

import "testing"

func TestSystemParsers(t *testing.T) {
	first, err := parseCPUSample([]byte("cpu  100 0 50 850 0 0 0 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseCPUSample([]byte("cpu  120 0 60 920 0 0 0 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cpuPercent(first, second); got != 30 {
		t.Fatalf("expected 30%% CPU, got %d", got)
	}
	memory, err := parseMemoryPercent([]byte("MemTotal: 1000 kB\nMemAvailable: 580 kB\n"))
	if err != nil || memory != 42 {
		t.Fatalf("memory=%d err=%v", memory, err)
	}
	uptime, err := parseUptime([]byte("100800.25 20.0\n"))
	if err != nil || uptime != 100800 {
		t.Fatalf("uptime=%d err=%v", uptime, err)
	}
}

func TestSystemProviderShape(t *testing.T) {
	provider := NewSystemProvider()
	provider.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/proc/stat":
			return []byte("cpu  10 0 10 80 0 0 0 0\n"), nil
		case "/proc/meminfo":
			return []byte("MemTotal: 1000 kB\nMemAvailable: 500 kB\n"), nil
		default:
			return []byte("86400.5 0\n"), nil
		}
	}
	provider.hostname = func() (string, error) { return "pi", nil }
	provider.disk = func(string) (int, error) { return 31, nil }
	data, err := provider.Data(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	system := data["system"].(map[string]any)
	if system["hostname"] != "pi" || system["memory"] != 50 || system["disk"] != 31 || system["uptime"] != int64(86400) {
		t.Fatalf("unexpected data: %+v", system)
	}
}
