package nodecfg

import (
	"fmt"
	"net"
	"strings"
)

// The manager interface (AMI) card and its checks (docs/CONFIG.md, "Manager
// interface"). The [admin] secret is never carried in the model.

// Codes of the AMI findings.
const (
	CodeAMIDisabled = "ami_disabled"
	CodeAMIExposed  = "ami_exposed"
	CodeAMIPort     = "ami_port_changed"
)

// AMIInfo is the state of manager.conf as the overview shows it.
type AMIInfo struct {
	Present  bool   `json:"present"`
	Enabled  string `json:"enabled,omitempty"`
	BindAddr string `json:"bindaddr,omitempty"`
	Port     string `json:"port,omitempty"`
	// HasSecret: [admin] has a secret (its value is never sent).
	HasSecret bool `json:"hasSecret"`
	// Loopback: the interface listens on this computer only.
	Loopback bool `json:"loopback"`
	// Fields are the schema keys of the form (secret masked).
	Fields []Field `json:"fields,omitempty"`
}

func (w *World) amiValue(section, key string) (string, bool) {
	t := w.Trees[FileManager]
	if t == nil {
		return "", false
	}
	defs := t.Lookup(section)
	if len(defs) == 0 {
		return "", false
	}
	if e := lastEntry(defs[0].Entries, key); e != nil {
		return strings.TrimSpace(e.Value), true
	}
	return "", false
}

// isLoopback reports whether an AMI bindaddr (host or host:port) is on this
// computer only. An empty or unspecified address listens everywhere.
func isLoopback(addr string) bool {
	h := addr
	if host, _, err := net.SplitHostPort(addr); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (w *World) ami() AMIInfo {
	a := AMIInfo{Present: w.Has(FileManager)}
	if !a.Present {
		return a
	}
	a.Enabled, _ = w.amiValue("general", "enabled")
	a.BindAddr, _ = w.amiValue("general", "bindaddr")
	a.Port, _ = w.amiValue("general", "port")
	s, _ := w.amiValue("admin", "secret")
	a.HasSecret = s != ""
	// Asterisk's default bindaddr is all interfaces.
	a.Loopback = a.BindAddr != "" && isLoopback(a.BindAddr)
	return a
}

// amiFindings are the checks on manager.conf. enabled = no is an error: ASLCP
// and every other tool would lose the interface.
func (w *World) amiFindings() []Finding {
	if !w.Has(FileManager) {
		return nil
	}
	var out []Finding
	a := w.ami()
	if a.Enabled != "" && !isYes(a.Enabled) {
		out = append(out, Finding{Level: LevelError, Code: CodeAMIDisabled, File: FileManager,
			Message: "The manager interface would be turned off. ASLCP and the other tools that use it would stop working."})
	}
	if !a.Loopback {
		where := "all network interfaces"
		if a.BindAddr != "" {
			where = a.BindAddr
		}
		out = append(out, Finding{Level: LevelWarning, Code: CodeAMIExposed, File: FileManager,
			Message: fmt.Sprintf("The manager interface listens on %s, not only on this computer. Anyone who can reach it and knows the password controls Asterisk. Use 127.0.0.1 unless another computer needs it, and keep the password secret.", where)})
	}
	return out
}

func isYes(v string) bool {
	switch strings.ToLower(v) {
	case "yes", "true", "1", "on":
		return true
	}
	return false
}
