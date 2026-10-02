package nodes

import (
	"context"
	"sync"
)

type NodeContext struct {
	NodeID    string
	NodeType  string
	SessionID string
	Node      Node
}

type contextKey struct{}

func WithContext(ctx context.Context, n Node, sessionID string) context.Context {
	return context.WithValue(ctx, contextKey{}, NodeContext{NodeID: n.ID, NodeType: n.Type, SessionID: sessionID, Node: clone(n)})
}
func FromContext(ctx context.Context) (NodeContext, bool) {
	n, ok := ctx.Value(contextKey{}).(NodeContext)
	return n, ok
}

// State keeps application and session values isolated by authenticated node.
// A future persistent implementation can keep this interface unchanged.
type State struct {
	mu     sync.RWMutex
	values map[string]any
}

func NewState() *State { return &State{values: map[string]any{}} }
func stateKey(nodeID, namespace, key string) string {
	return nodeID + "\x00" + namespace + "\x00" + key
}
func (s *State) Get(nodeID, namespace, key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[stateKey(nodeID, namespace, key)]
	return v, ok
}
func (s *State) Set(nodeID, namespace, key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[stateKey(nodeID, namespace, key)] = value
}
func (s *State) Delete(nodeID, namespace, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, stateKey(nodeID, namespace, key))
}
func (s *State) GetSession(nodeID, sessionID, key string) (any, bool) {
	return s.Get(nodeID, "session:"+sessionID, key)
}
func (s *State) SetSession(nodeID, sessionID, key string, value any) {
	s.Set(nodeID, "session:"+sessionID, key, value)
}
