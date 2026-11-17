package diag

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Link is one link of a local node (no peer IP).
type Link struct {
	Node      string
	Mode      string
	State     string
	Direction string
	Kind      string
}

// LocalNode is the monitor state of one local node.
type LocalNode struct {
	Node     string
	Callsign string // from the node list, "" when unknown
	NumLinks int
	Links    []Link
	Stale    bool
	StaleErr string
	Updated  time.Time
}

// Report holds everything the diagnostics file shows.
type Report struct {
	Now      time.Time
	Version  string
	Asterisk string
	Platform Platform
	Username string // not printed; reserved

	Uptime       int64 // host seconds
	Load         [3]float64
	MemTotal     uint64
	MemAvailable uint64
	CPUTemp      *float64

	AMIConnected bool
	AMIError     string
	AMISince     time.Time
	AMIAddr      string

	Nodes []LocalNode

	NodelistCount   int
	NodelistSource  string
	NodelistUpdated *time.Time
	NodelistError   string

	UpdatesAvailable bool
	UpdatesReason    string
	UpdatesInstalled string
	UpdatesCandidate string
	UpdatesJob       string
	UpdatesJobLog    string
	UpdatesChecked   string

	// Helper / firewall (HelperErr set when the helper cannot be reached).
	HelperErr       string
	FirewallPresent bool
	FirewallRunning bool
	FirewallEnabled bool
	HaveFirewall    bool

	// Config lines (already secret-masked) and log lines.
	Config []string
	Log    []string

	// Redaction.
	Secrets []string
}

func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orNA(s string) string {
	if strings.TrimSpace(s) == "" {
		return "n/a"
	}
	return s
}

// KeepCalls returns the callsigns of the local nodes (kept by Redact).
func (r Report) KeepCalls() []string {
	var out []string
	for _, n := range r.Nodes {
		if n.Callsign != "" {
			out = append(out, n.Callsign)
		}
	}
	return out
}

// Render produces the plain-text report and runs it through Redact.
func (r Report) Render() string {
	var b strings.Builder
	p := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	h := func(t string) { p("\n== %s ==", t) }

	p("ASLCP diagnostics")
	p("Generated: %s", r.Now.UTC().Format(time.RFC3339))
	p("Private data (IP addresses, secrets, emails, other stations' callsigns) is removed from this file; see the ASLCP docs.")

	h("Versions and platform")
	p("ASLCP:    %s", orNA(r.Version))
	p("Asterisk: %s", orNA(r.Asterisk))
	p("OS:       %s", orNA(r.Platform.Distro))
	p("Kernel:   %s", orNA(r.Platform.Kernel))
	p("Arch:     %s/%s", r.Platform.GOOS, r.Platform.GOARCH)
	p("Hardware: %s (%s)", orNA(r.Platform.Model), r.Platform.Hardware)

	h("Host")
	p("Uptime: %ds", r.Uptime)
	p("Load:   %.2f %.2f %.2f", r.Load[0], r.Load[1], r.Load[2])
	p("Memory: %d MiB available of %d MiB", r.MemAvailable>>20, r.MemTotal>>20)
	if r.CPUTemp != nil {
		p("CPU temperature: %.1f C", *r.CPUTemp)
	} else {
		p("CPU temperature: n/a")
	}

	h("Asterisk Manager Interface")
	p("Address:   %s", orNA(r.AMIAddr))
	p("Connected: %s", yesno(r.AMIConnected))
	if !r.AMISince.IsZero() {
		p("State since: %s", r.AMISince.UTC().Format(time.RFC3339))
	}
	if r.AMIError != "" {
		p("Last error: %s", r.AMIError)
	}

	h("Local nodes")
	if len(r.Nodes) == 0 {
		p("(none known)")
	}
	for _, n := range r.Nodes {
		call := n.Callsign
		if call == "" {
			call = "unknown callsign"
		}
		p("Node %s (%s): links=%d stale=%s", n.Node, call, n.NumLinks, yesno(n.Stale))
		if n.StaleErr != "" {
			p("  stale reason: %s", n.StaleErr)
		}
		if !n.Updated.IsZero() {
			p("  last poll: %s", n.Updated.UTC().Format(time.RFC3339))
		}
		modes := map[string]int{}
		for _, l := range n.Links {
			modes[l.Mode]++
		}
		keys := make([]string, 0, len(modes))
		for k := range modes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var ms []string
		for _, k := range keys {
			ms = append(ms, fmt.Sprintf("%s=%d", orNA(k), modes[k]))
		}
		if len(ms) > 0 {
			p("  modes: %s", strings.Join(ms, " "))
		}
		for _, l := range n.Links {
			p("  link %s mode=%s state=%s dir=%s kind=%s", l.Node, orNA(l.Mode), orNA(l.State), orNA(l.Direction), orNA(l.Kind))
		}
	}

	h("Node list")
	p("Entries: %d  source: %s", r.NodelistCount, orNA(r.NodelistSource))
	if r.NodelistUpdated != nil {
		p("Updated: %s", r.NodelistUpdated.UTC().Format(time.RFC3339))
	} else {
		p("Updated: never")
	}
	if r.NodelistError != "" {
		p("Error: %s", r.NodelistError)
	}

	h("Updates and packages")
	p("Helper available: %s", yesno(r.UpdatesAvailable))
	if r.UpdatesReason != "" {
		p("Reason: %s", r.UpdatesReason)
	}
	if r.UpdatesAvailable {
		p("Installed: %s  candidate: %s  job: %s  last check: %s", orNA(r.UpdatesInstalled), orNA(r.UpdatesCandidate), orNA(r.UpdatesJob), orNA(r.UpdatesChecked))
		if strings.TrimSpace(r.UpdatesJobLog) != "" {
			p("Update log:")
			for _, l := range strings.Split(strings.TrimRight(r.UpdatesJobLog, "\n"), "\n") {
				p("  %s", l)
			}
		}
	}
	switch {
	case r.HelperErr != "":
		p("Firewall (helper): unavailable: %s", r.HelperErr)
	case r.HaveFirewall:
		p("Firewall (helper): firewall-cmd installed=%s firewalld running=%s aslcp service enabled=%s", yesno(r.FirewallPresent), yesno(r.FirewallRunning), yesno(r.FirewallEnabled))
	default:
		p("Firewall (helper): n/a")
	}

	h("Configuration (secrets masked)")
	if len(r.Config) == 0 {
		p("(none)")
	}
	for _, l := range r.Config {
		p("%s", l)
	}

	h(fmt.Sprintf("Server log (last %d lines)", len(r.Log)))
	if len(r.Log) == 0 {
		p("(empty)")
	}
	for _, l := range r.Log {
		p("%s", l)
	}

	return Redact(b.String(), Options{Secrets: r.Secrets, KeepCalls: r.KeepCalls()})
}
