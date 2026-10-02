package auth

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/n0remac/Fabric/internal/nodes"
)

var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type browserSession struct {
	nodeID  string
	expires time.Time
}
type Middleware struct {
	Registry *nodes.Registry
	mu       sync.Mutex
	sessions map[string]browserSession
}
type Operation func(*http.Request) (capability, resource string)

func Fixed(capability string) Operation {
	return func(r *http.Request) (string, string) { return capability, r.PathValue("id") }
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.ResponseWriter.Write(b)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	conn, rw, err := h.Hijack()
	if err == nil {
		w.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}

func (m *Middleware) Require(operation Operation, next http.Handler) http.Handler {
	return m.require(operation, next, false)
}
func (m *Middleware) RequireBrowser(operation Operation, next http.Handler) http.Handler {
	return m.require(operation, next, true)
}
func (m *Middleware) require(operation Operation, next http.Handler, browser bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capability, resource := operation(r)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Vary", "Authorization, Cookie")
		if m == nil || m.Registry == nil {
			deny(w, browser)
			return
		}
		var n nodes.Node
		var ok bool
		var sessionID string
		values := r.Header.Values("Authorization")
		if len(values) == 1 && strings.HasPrefix(values[0], "Bearer ") {
			n, ok = m.Registry.Authenticate(strings.TrimPrefix(values[0], "Bearer "))
		}
		if browser && !ok && len(values) == 1 {
			if _, token, valid := r.BasicAuth(); valid {
				n, ok = m.Registry.Authenticate(token)
				if ok {
					if c, err := r.Cookie("fabric_sim"); err == nil {
						if existing, valid := m.sessionNode(c.Value); valid && existing.ID == n.ID {
							sessionID = c.Value
						}
					}
					if sessionID == "" {
						sessionID = m.newSession(w, r, n.ID)
					}
				}
			}
		}
		if browser && !ok && len(values) == 0 {
			if c, err := r.Cookie("fabric_sim"); err == nil {
				n, ok = m.sessionNode(c.Value)
				if ok {
					sessionID = c.Value
				}
			}
		}
		if !ok {
			deny(w, browser)
			log.Printf("node=unknown action=%q resource=%q outcome=unauthorized", capability, resource)
			return
		}
		if browser && len(values) == 1 && strings.HasPrefix(values[0], "Basic ") && sessionID == "" {
			http.Error(w, "could not create browser session", 500)
			return
		}
		if !nodes.Has(n, capability) {
			if browser {
				http.Error(w, "forbidden", 403)
			} else {
				apiError(w, 403, "forbidden")
			}
			log.Printf("node=%q action=%q resource=%q outcome=denied", n.ID, capability, resource)
			return
		}
		if !browser {
			sessionID = r.Header.Get("X-Fabric-Session")
		}
		if sessionID != "" && !sessionIDPattern.MatchString(sessionID) {
			if browser {
				http.Error(w, "invalid session identifier", 400)
			} else {
				apiError(w, 400, "invalid_session")
			}
			return
		}
		wrapped := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r.WithContext(nodes.WithContext(r.Context(), n, sessionID)))
		status := wrapped.status
		if status == 0 {
			status = 200
		}
		log.Printf("node=%q action=%q resource=%q status=%d outcome=completed", n.ID, capability, resource, status)
	})
}
func (m *Middleware) newSession(w http.ResponseWriter, r *http.Request, nodeID string) string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	id := hex.EncodeToString(raw[:])
	m.mu.Lock()
	if m.sessions == nil {
		m.sessions = map[string]browserSession{}
	}
	for key, session := range m.sessions {
		if time.Now().After(session.expires) {
			delete(m.sessions, key)
		}
	}
	m.sessions[id] = browserSession{nodeID: nodeID, expires: time.Now().Add(12 * time.Hour)}
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "fabric_sim", Value: id, Path: "/", HttpOnly: true, Secure: os.Getenv("ENVIRONMENT") == "production" || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600})
	return id
}
func (m *Middleware) sessionNode(id string) (nodes.Node, bool) {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if ok && time.Now().After(session.expires) {
		delete(m.sessions, id)
		ok = false
	}
	m.mu.Unlock()
	if !ok {
		return nodes.Node{}, false
	}
	n, exists := m.Registry.Get(session.nodeID)
	return n, exists && n.Enabled
}
func deny(w http.ResponseWriter, browser bool) {
	if browser {
		w.Header().Set("WWW-Authenticate", `Basic realm="Fabric simulator"`)
		http.Error(w, "unauthorized", 401)
	} else {
		w.Header().Set("WWW-Authenticate", `Bearer realm="fabric"`)
		apiError(w, 401, "unauthorized")
	}
}
func apiError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": code})
}
