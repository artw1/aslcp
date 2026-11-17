package rpt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/ami/amitest"
)

// fakeNode is an amitest server that answers the app_rpt actions from
// mutable state.
type fakeNode struct {
	*amitest.Server

	mu       sync.Mutex
	node     string
	rx, tx   bool
	links    []string        // adjacent links, all transceive/established
	keyed    map[string]bool // per-link keyed (SawStat)
	commands []string
	cmdOut   map[string][]string // CLI command -> output lines
	failNode bool                // answer XStat with Response: Error
	xstats   int
}

func newFakeNode(t *testing.T, node string) *fakeNode {
	t.Helper()
	f := &fakeNode{Server: amitest.New(t), node: node, keyed: map[string]bool{}, cmdOut: map[string][]string{}}
	f.Handle("RptLocalNodes", func(_ *amitest.Session, r *amitest.Request) string {
		return amitest.Raw(`<?xml version="1.0"?>`, `<nodes>`, `  <node>`+f.node+`</node>`, `</nodes>`)
	})
	f.Handle("RptStatus", f.rptStatus)
	f.Handle("Command", func(_ *amitest.Session, r *amitest.Request) string {
		f.mu.Lock()
		defer f.mu.Unlock()
		cmd := r.Get("Command")
		f.commands = append(f.commands, cmd)
		return amitest.CommandOutput(r, f.cmdOut[cmd]...)
	})
	return f
}

func (f *fakeNode) set(fn func(f *fakeNode)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeNode) xstatCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.xstats
}

func (f *fakeNode) cmds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (f *fakeNode) rptStatus(_ *amitest.Session, r *amitest.Request) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Get("Node") != "" && r.Get("Node") != f.node {
		return amitest.Error(r, "RptStatus unknown or missing node")
	}
	switch r.Get("Command") {
	case "XStat":
		f.xstats++
		if f.failNode {
			return amitest.Error(r, "RptStatus unknown or missing node")
		}
		kv := []string{"Node", f.node}
		var net, al []string
		for _, l := range f.links {
			kv = append(kv, "Conn", fmt.Sprintf("%-10s%-20s%-12d%-11s%-20s%-20s", l, "", 0, "IN", "00:01:00", "ESTABLISHED"))
			net = append(net, "T"+l)
			al = append(al, l+"T"+map[bool]string{true: "K", false: "U"}[f.keyed[l]])
		}
		ln := "<NONE>"
		if len(net) > 0 {
			ln = strings.Join(net, ", ")
		}
		kv = append(kv, "LinkedNodes", ln,
			"Var", "RPT_RXKEYED="+b01(f.rx), "Var", "RPT_TXKEYED="+b01(f.tx), "Var", "RPT_ETXKEYED=0",
			"Var", fmt.Sprintf("RPT_NUMLINKS=%d", len(net)),
			"Var", fmt.Sprintf("RPT_NUMALINKS=%d", len(al)))
		if len(net) > 0 {
			kv = append(kv, "Var", fmt.Sprintf("RPT_LINKS=%d,%s", len(net), strings.Join(net, ",")),
				"Var", fmt.Sprintf("RPT_ALINKS=%d,%s", len(al), strings.Join(al, ",")))
		}
		kv = append(kv, "parrot_ena", "0", "sys_ena", "0", "tot_ena", "0", "link_ena", "0", "patch_ena", "1",
			"patch_state", "4", "sch_ena", "0", "user_funs", "0", "tail_type", "0", "iconns", "0",
			"tot_state", "2", "ider_state", "2", "tel_mode", "1")
		return amitest.Success(r, kv...)
	case "SawStat":
		kv := []string{"Node", f.node}
		for _, l := range f.links {
			if f.keyed[l] {
				kv = append(kv, "Conn", l+" 1 3 -1")
			} else {
				kv = append(kv, "Conn", l+" 0 -1 -1")
			}
		}
		return amitest.Success(r, kv...)
	case "RptStat":
		return amitest.Success(r, "Nodes", f.node)
	}
	return amitest.Error(r, "RptStatus unknown command")
}

// cfg returns a fast-reconnecting client config for the server.
func (f *fakeNode) cfg() ami.Config {
	return ami.Config{
		Addr: f.Addr, Username: f.Username, Secret: f.Secret, Events: "call",
		DialTimeout: 2 * time.Second, ReconnectMin: 20 * time.Millisecond, ReconnectMax: 80 * time.Millisecond,
	}
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
