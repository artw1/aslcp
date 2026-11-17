package rpt

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami"
)

// timedAPI records when each command arrives.
type timedAPI struct {
	mu   sync.Mutex
	cmds []string
	at   []time.Time
}

func (a *timedAPI) Action(context.Context, string, ...ami.Header) (*ami.Message, error) {
	return &ami.Message{}, nil
}

func (a *timedAPI) Command(_ context.Context, cmd string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cmds = append(a.cmds, cmd)
	a.at = append(a.at, time.Now())
	return nil, nil
}

// app_rpt refuses a command that arrives while the node's single command
// slot is still busy, so commands to one node must be spaced out.
func TestCommandsToOneNodeAreSpaced(t *testing.T) {
	api := &timedAPI{}
	c := New(api)
	c.MinGap = 150 * time.Millisecond
	ctx := context.Background()
	if err := c.Disconnect(ctx, "1999", "50241", false); err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(ctx, "1999", "2000", ConnTransceive, false); err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(ctx, "1998", "2001", ConnTransceive, false); err != nil {
		t.Fatal(err)
	}
	if len(api.at) != 3 {
		t.Fatalf("commands = %v", api.cmds)
	}
	if !strings.Contains(api.cmds[0], "ilink 1 50241") || !strings.Contains(api.cmds[1], "ilink 3 2000") {
		t.Fatalf("order = %v", api.cmds)
	}
	if gap := api.at[1].Sub(api.at[0]); gap < 150*time.Millisecond {
		t.Errorf("same-node commands only %v apart", gap)
	}
	if gap := api.at[2].Sub(api.at[1]); gap > 100*time.Millisecond {
		t.Errorf("a different node waited %v", gap)
	}
}

func TestPacingRespectsContext(t *testing.T) {
	c := New(&timedAPI{})
	c.MinGap = time.Hour
	if err := c.Disconnect(context.Background(), "1999", "50241", false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Disconnect(ctx, "1999", "2000", false); err == nil {
		t.Fatal("expected context error while waiting for the gap")
	}
}
