package api

import (
	"context"
	"sync"
	"time"
)

// publicBridge gives the public status page the private DVSwitch bridge node
// and its display name, cached briefly (the lookup reads rpt.conf). nil when
// the bridge lookup is not configured.
func (s *Server) publicBridge() func() (string, string) {
	if s.d.DVBridgeNode == nil {
		return nil
	}
	var (
		mu   sync.Mutex
		at   time.Time
		node string
	)
	return func() (string, string) {
		mu.Lock()
		defer mu.Unlock()
		if at.IsZero() || time.Since(at) > 30*time.Second {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			n, ok := s.d.DVBridgeNode(ctx)
			cancel()
			node = ""
			if ok {
				node = n
			}
			at = time.Now()
		}
		if node == "" {
			return "", ""
		}
		return node, s.dvNodeName()
	}
}
