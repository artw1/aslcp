package helper

import (
	"context"
	"errors"
	"strings"
)

// System tab ops (docs/PLAN-1.3.0.md). One file per feature registers its
// ops in sysOps from an init function, so a new sub-section adds a file and
// one field to SysRequest, and touches nothing else in the dispatcher.
//
// Like every other op these take no free-form arguments: each field of
// SysRequest is typed, validated by the op that reads it, and reaches a
// command line only as one argv item (never through a shell).

// SysRequest is the argument of the System tab ops. Every op reads only the
// fields it documents and ignores the rest.
type SysRequest struct {
	Hostname string `json:"hostname,omitempty"` // host-set-hostname
	Timezone string `json:"timezone,omitempty"` // host-set-timezone
	NTP      *bool  `json:"ntp,omitempty"`      // host-set-ntp
	Unit     string `json:"unit,omitempty"`     // logs-read
	Lines    int    `json:"lines,omitempty"`    // logs-read
	Since    string `json:"since,omitempty"`    // logs-read

	// svc-set (sys_svc.go): scope and action are checked against fixed sets.
	SvcScope    string `json:"svcScope,omitempty"`
	SvcAction   string `json:"svcAction,omitempty"`
	SvcFirewall bool   `json:"svcFirewall,omitempty"`
	// web-port-set (sys_webport.go): one of the WebPortAction* constants.
	WebPortAction string `json:"webPortAction,omitempty"`
	// wifi-* (sys_wifi.go).
	Wifi *WifiRequest `json:"wifi,omitempty"`
}

type sysHandler func(o *Ops, ctx context.Context, req *SysRequest) (any, error)

// sysOps maps an op name to its handler; filled by init functions only.
var sysOps = map[string]sysHandler{}

func registerSysOp(op string, h sysHandler) {
	if _, dup := sysOps[op]; dup {
		panic("helper: duplicate sys op " + op)
	}
	sysOps[op] = h
}

func isSysOp(op string) bool { _, ok := sysOps[op]; return ok }

func (o *Ops) handleSys(ctx context.Context, req Request) (any, error) {
	h, ok := sysOps[req.Op]
	if !ok {
		return nil, errors.New("unknown op")
	}
	sr := req.Sys
	if sr == nil {
		sr = &SysRequest{}
	}
	return h(o, ctx, sr)
}

// parseKV parses "Key=Value" lines (systemctl show, timedatectl show).
// Lines without "=" are ignored; the last value wins.
func parseKV(text string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if i := strings.IndexByte(line, '='); i > 0 {
			m[line[:i]] = line[i+1:]
		}
	}
	return m
}
