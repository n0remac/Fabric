package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/n0remac/Fabric/internal/actions"
	"github.com/n0remac/Fabric/internal/api"
	"github.com/n0remac/Fabric/internal/auth"
	"github.com/n0remac/Fabric/internal/fabric"
	"github.com/n0remac/Fabric/internal/firmware"
	"github.com/n0remac/Fabric/internal/nodes"
	"github.com/n0remac/Fabric/internal/pages"
	"github.com/n0remac/Fabric/internal/providers"
	"github.com/n0remac/Fabric/internal/simulator"
	ws "github.com/n0remac/GoDom/websocket"
)

var roomPattern = regexp.MustCompile(`^simulator-[a-z][a-z0-9_-]{0,63}$`)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	address := flag.String("addr", envOrDefault("FABRIC_ADDR", ":8080"), "HTTP listen address")
	pagesDirectory := flag.String("pages", envOrDefault("FABRIC_PAGES_DIR", "pages"), "Fabric Page directory")
	firmwareDirectory := flag.String("firmware", envOrDefault("FABRIC_FIRMWARE_DIR", ""), "Firmware directory (empty disables firmware)")
	nodesFile := flag.String("nodes", envOrDefault("FABRIC_NODES_FILE", ""), "Node registry file (required)")
	flag.Parse()
	if *nodesFile == "" {
		return errors.New("FABRIC_NODES_FILE or -nodes is required")
	}
	nodeRegistry, err := nodes.Open(*nodesFile, false)
	if err != nil {
		return fmt.Errorf("node registry: %w", err)
	}
	identity := &auth.Middleware{Registry: nodeRegistry}

	var firmwareHandler *firmware.Handler
	if *firmwareDirectory != "" {
		store, err := firmware.NewStore(*firmwareDirectory)
		if err != nil {
			return fmt.Errorf("firmware registry: %w", err)
		}
		defer store.Close()
		firmwareHandler = &firmware.Handler{Store: store, Auth: identity}
	}

	actionRegistry := actions.NewRegistry()
	providerRegistry := providers.NewRegistry()
	if err := providerRegistry.Register(providers.NewSystemProvider()); err != nil {
		return err
	}
	symbols, err := providers.ParseStockTickers(os.Getenv("FABRIC_STOCK_TICKERS"))
	if err != nil {
		return err
	}
	stockProvider, err := providers.NewStockProvider(providers.NewYahooChartSource(), symbols)
	if err != nil {
		return err
	}
	if err := providerRegistry.Register(stockProvider); err != nil {
		return err
	}
	if err := actionRegistry.RegisterResult("stocks.select", func(ctx context.Context, args map[string]any) (string, error) {
		symbol, ok := args["symbol"].(string)
		if !ok {
			return "", errors.New("stock selection requires a symbol")
		}
		if err := stockProvider.SelectFor(ctx, symbol); err != nil {
			return "", err
		}
		return "stock-detail", nil
	}); err != nil {
		return err
	}
	for _, period := range []providers.Period{providers.Period1D, providers.Period5D, providers.Period1M, providers.Period1Y} {
		period := period
		if err := actionRegistry.Register("stocks.period."+string(period), func(ctx context.Context, _ map[string]any) error { return stockProvider.SetPeriodFor(ctx, period) }); err != nil {
			return err
		}
	}
	validator, err := fabric.NewValidator(actionRegistry, providerRegistry)
	if err != nil {
		return err
	}
	pageStore, err := pages.NewStore(*pagesDirectory, validator, pages.StockWatchlistTransform(symbols))
	if err != nil {
		return err
	}
	dispatcher := &actions.Dispatcher{Pages: pageStore, Providers: providerRegistry, Actions: actionRegistry}
	renderer := simulator.NewGoDomRenderer()

	websocketConfig, err := websocketConfig(*address)
	if err != nil {
		return err
	}
	hub, err := ws.NewHub(websocketConfig)
	if err != nil {
		return fmt.Errorf("create websocket hub: %w", err)
	}
	commandRegistry := ws.NewRegistry()

	mux := http.NewServeMux()
	if firmwareHandler != nil {
		firmwareHandler.Mount(mux)
	}
	(&api.Handler{Auth: identity, Pages: pageStore, Providers: providerRegistry, Dispatcher: dispatcher}).Mount(mux)
	(&simulator.Handler{Auth: identity, Pages: pageStore, Providers: providerRegistry, Dispatcher: dispatcher, Renderer: renderer}).Mount(mux)
	websocketHandler := ws.Handler(hub, commandRegistry, ws.Hooks{})
	if os.Getenv("ENVIRONMENT") != "production" && len(splitList(os.Getenv("WEBSOCKET_ALLOWED_ORIGINS"))) == 0 {
		websocketHandler = allowSameOriginWebSocket(websocketHandler, websocketConfig.AllowedOrigins[0])
	}
	mux.Handle("GET /ws/hub", identity.RequireBrowser(auth.Fixed("pages.read"), websocketHandler))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/simulator", http.StatusTemporaryRedirect)
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go hub.Run(ctx)
	go pageStore.Watch(ctx, time.Second, func(err error) { log.Printf("page reload rejected; retaining last-known-good pages: %v", err) })
	go simulator.BroadcastChanges(ctx, pageStore, providerRegistry, renderer, hub)

	server := &http.Server{
		Addr:              *address,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	interfaceAddresses, err := net.InterfaceAddrs()
	if err != nil {
		log.Printf("could not discover server IP addresses: %v", err)
	}
	log.Printf("Fabric listening on %s with pages from %s", listener.Addr(), pageStore.Directory())
	for _, serverURL := range serverURLs(listener.Addr().String(), interfaceAddresses) {
		log.Printf("Fabric available at %s", serverURL)
	}
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func serverURLs(address string, interfaceAddresses []net.Addr) []string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil
	}
	ip := net.ParseIP(host)
	if host != "" && (ip == nil || !ip.IsUnspecified()) {
		return []string{"http://" + net.JoinHostPort(host, port)}
	}
	hosts := []string{"127.0.0.1"}
	seen := map[string]bool{"127.0.0.1": true}
	for _, address := range interfaceAddresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err != nil || !ip.IsGlobalUnicast() {
			continue
		}
		if host == "0.0.0.0" && ip.To4() == nil {
			continue
		}
		if value := ip.String(); !seen[value] {
			seen[value] = true
			hosts = append(hosts, value)
		}
	}
	urls := make([]string, 0, len(hosts))
	for _, host := range hosts {
		urls = append(urls, "http://"+net.JoinHostPort(host, port))
	}
	return urls
}

func websocketConfig(address string) (ws.Config, error) {
	config := ws.DefaultConfig()
	config.ValidateRoomID = func(room string) error {
		if !roomPattern.MatchString(room) {
			return errors.New("invalid simulator room")
		}
		return nil
	}
	origins := splitList(os.Getenv("WEBSOCKET_ALLOWED_ORIGINS"))
	if os.Getenv("ENVIRONMENT") == "production" {
		if len(origins) == 0 {
			return ws.Config{}, errors.New("WEBSOCKET_ALLOWED_ORIGINS is required in production")
		}
		config.AllowedOrigins = origins
		config.AllowMissingOrigin = false
		return config, nil
	}
	if len(origins) == 0 {
		port := "8080"
		if _, after, ok := strings.Cut(address, ":"); ok && after != "" {
			port = after
		}
		origins = []string{"http://localhost:" + port, "http://127.0.0.1:" + port}
	}
	config.AllowedOrigins = origins
	config.AllowMissingOrigin = true
	return config, nil
}

// GoDom uses a fixed origin allowlist. For the default development setup,
// admit a browser connecting back to the same host it used for the page (for
// example, a Pi's Tailscale IP) before passing the request to that allowlist.
func allowSameOriginWebSocket(next http.Handler, allowedOrigin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sameOriginHost(r.Header.Get("Origin"), r.Host) {
			request := r.Clone(r.Context())
			request.Header.Set("Origin", allowedOrigin)
			next.ServeHTTP(w, request)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOriginHost(rawOrigin, requestHost string) bool {
	if rawOrigin == "" || requestHost == "" {
		return false
	}
	origin, err := url.Parse(rawOrigin)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	return strings.EqualFold(origin.Host, requestHost)
}

func splitList(value string) []string {
	var output []string
	seen := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		output = append(output, item)
	}
	return output
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self' ws: wss:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
