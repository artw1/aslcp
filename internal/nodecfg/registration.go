package nodecfg

import (
	"regexp"
	"strings"

	"github.com/artw1/aslcp/internal/astconf"
)

// DefaultRegHost is the host a registration points at unless told otherwise.
const DefaultRegHost = "register.allstarlink.org"

// Registration methods.
const (
	RegNone = "none"
	RegHTTP = "http"
	RegIAX  = "iax"
	RegBoth = "both" // a problem: the node is registered both ways
)

// RegLine is one `register =>` line. The password is never carried.
type RegLine struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Node        string `json:"node"`
	Host        string `json:"host"`
	HasPassword bool   `json:"hasPassword"`
	// Placeholder: the stock sample line shipped with ASL3.
	Placeholder bool `json:"placeholder,omitempty"`

	password string
}

// RegStatus is the last registration state obtained from Asterisk. It is only
// filled when the caller asks for it (it costs an AMI command).
type RegStatus struct {
	// State is "registered", "failed" or "unknown".
	State string `json:"state"`
	// Detail is the matching line of Asterisk's reply, without addresses.
	Detail string `json:"detail,omitempty"`
}

// Registration is how a node registers with the AllStar network.
type Registration struct {
	// Method is "http", "iax", "both" or "none".
	Method string `json:"method"`
	// Http and Iax are the node's lines (nil: none).
	Http *RegLine `json:"http,omitempty"`
	Iax  *RegLine `json:"iax,omitempty"`
	// HttpFile is where an HTTP registration is written ("" when
	// rpt_http_registrations.conf does not exist).
	HttpFile string     `json:"httpFile,omitempty"`
	Host     string     `json:"host,omitempty"`
	Status   *RegStatus `json:"status,omitempty"`
}

var reRegValue = regexp.MustCompile(`^([^:@\s]+)(?::([^@]*))?@(\S+)$`)

// parseRegister splits `node:password@host` (the value after `register =>`).
func parseRegister(v string) (node, pass, host string, ok bool) {
	v = strings.TrimSpace(v)
	m := reRegValue.FindStringSubmatch(v)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}

func formatRegister(node, pass, host string) string { return node + ":" + pass + "@" + host }

// regLines returns every well-formed register line of the tree.
func regLines(t *astconf.Tree) []RegLine {
	if t == nil {
		return nil
	}
	var out []RegLine
	for _, s := range t.Sections {
		for _, e := range s.Entries {
			if !strings.EqualFold(e.Key, "register") {
				continue
			}
			n, p, h, ok := parseRegister(e.Value)
			if !ok {
				continue
			}
			out = append(out, RegLine{File: e.File, Line: e.Line, Node: n, Host: h, HasPassword: p != "",
				Placeholder: n == "1234" && p == "abcdef", password: p})
		}
	}
	return out
}

func (w *World) registration(node string) Registration {
	r := Registration{Method: RegNone}
	for _, l := range regLines(w.Trees[FileHTTPReg]) {
		if l.Node == node && r.Http == nil {
			l := l
			r.Http = &l
		}
	}
	for _, l := range regLines(w.Trees[FileIAX]) {
		if l.Node == node && r.Iax == nil {
			l := l
			r.Iax = &l
		}
	}
	if w.Has(FileHTTPReg) {
		r.HttpFile = FileHTTPReg
	}
	switch {
	case r.Http != nil && r.Iax != nil:
		r.Method = RegBoth
	case r.Http != nil:
		r.Method = RegHTTP
	case r.Iax != nil:
		r.Method = RegIAX
	}
	switch {
	case r.Http != nil:
		r.Host = r.Http.Host
	case r.Iax != nil:
		r.Host = r.Iax.Host
	}
	return r
}

// ParseRegStatus reads the reply of `rpt show registrations` (method http) or
// `iax2 show registry` (method iax) for the node. The HTTP reply format has
// not been checked on hardware (docs/CONFIG.md): anything that is not clearly
// registered or clearly failed is "unknown".
func ParseRegStatus(method, node string, lines []string) RegStatus {
	for _, raw := range lines {
		if !strings.Contains(raw, node) {
			continue
		}
		l := strings.ToLower(raw)
		st := "unknown"
		switch {
		case strings.Contains(l, "unregistered"), strings.Contains(l, "rejected"), strings.Contains(l, "timeout"),
			strings.Contains(l, "failed"), strings.Contains(l, "no authentication"):
			st = "failed"
		case strings.Contains(l, "registered"):
			st = "registered"
		}
		return RegStatus{State: st, Detail: scrubDetail(raw)}
	}
	return RegStatus{State: "unknown"}
}

var reIPish = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b`)

// scrubDetail drops addresses from a reply line.
func scrubDetail(s string) string {
	s = reIPish.ReplaceAllString(s, "…")
	return strings.Join(strings.Fields(s), " ")
}
