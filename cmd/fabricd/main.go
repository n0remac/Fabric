package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/n0remac/Fabric/internal/actions"
	"github.com/n0remac/Fabric/internal/api"
	"github.com/n0remac/Fabric/internal/fabric"
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
	flag.Parse()

	actionRegistry := actions.NewRegistry()
	providerRegistry := providers.NewRegistry()
	if err := providerRegistry.Register(providers.NewSystemProvider()); err != nil {
		return err
	}
	validator, err := fabric.NewValidator(actionRegistry, providerRegistry)
	if err != nil {
		return err
	}
	pageStore, err := pages.NewStore(*pagesDirectory, validator)
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
	(&api.Handler{Pages: pageStore, Providers: providerRegistry, Dispatcher: dispatcher}).Mount(mux)
	(&simulator.Handler{Pages: pageStore, Providers: providerRegistry, Dispatcher: dispatcher, Renderer: renderer}).Mount(mux)
	mux.Handle("GET /ws/hub", ws.Handler(hub, commandRegistry, ws.Hooks{}))
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
	go func() {
		log.Printf("Fabric listening on %s with pages from %s", *address, pageStore.Directory())
		serverErrors <- server.ListenAndServe()
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
