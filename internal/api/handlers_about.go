package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/config"
	"github.com/artw1/aslcp/internal/diag"
	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/store"
)

// AboutJSON is the API object of GET /api/about.
type AboutJSON struct {
	Version  string        `json:"version"`
	Asterisk string        `json:"asterisk"`
	Platform diag.Platform `json:"platform"`
}

// AddSecret registers a literal (the resolved AMI secret) that diagnostics
// removes wherever it occurs.
func (s *Server) AddSecret(v string) {
	if v == "" {
		return
	}
	s.secretsMu.Lock()
	s.secrets = append(s.secrets, v)
	s.secretsMu.Unlock()
}

func (s *Server) knownSecrets() []string {
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	out := append([]string(nil), s.secrets...)
	if v := s.d.Config.AMISecret; v != "" && v != "auto" {
		out = append(out, v)
	}
	return out
}

func (s *Server) platform() diag.Platform {
	s.platOnce.Do(func() {
		p := diag.DefaultPlatformPaths
		if s.d.PlatformPaths != nil {
			p = *s.d.PlatformPaths
		}
		s.plat = diag.ReadPlatform(p)
	})
	return s.plat
}

func (s *Server) asteriskVersion(ctx context.Context) string {
	if s.d.System == nil {
		return ""
	}
	return s.d.System.AsteriskVersion(ctx)
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request, _ store.User) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	writeJSON(w, 200, AboutJSON{Version: s.d.Version, Asterisk: s.asteriskVersion(ctx), Platform: s.platform()})
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request, u store.User) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	rep := s.buildReport(ctx)
	s.d.Logger.Printf("diagnostics downloaded by %s", u.Username)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="aslcp-diagnostics-%s.txt"`, rep.Now.Format("2006-01-02")))
	_, _ = w.Write([]byte(rep.Render()))
}

func (s *Server) buildReport(ctx context.Context) diag.Report {
	rep := diag.Report{Now: time.Now(), Version: s.d.Version, Platform: s.platform(), Secrets: s.knownSecrets()}
	if s.d.System != nil {
		sys := s.d.System.Collect(ctx)
		rep.Asterisk = sys.Asterisk.Version
		rep.Uptime, rep.Load, rep.CPUTemp = sys.Uptime, sys.Load, sys.CPUTemp
		rep.MemTotal, rep.MemAvailable = sys.MemTotal, sys.MemAvailable
	}
	rep.AMIAddr = s.d.Config.AMIAddr()
	if s.d.AMI != nil {
		st := s.d.AMI.State()
		rep.AMIConnected, rep.AMISince = st.Connected, st.Since
		if st.Err != nil {
			rep.AMIError = st.Err.Error()
		}
	}
	if m := s.monitor(); m != nil {
		snap := m.Snapshot()
		nodes := make([]string, 0, len(snap))
		for n := range snap {
			nodes = append(nodes, n)
		}
		sort.Strings(nodes)
		for _, n := range nodes {
			st := snap[n]
			ln := diag.LocalNode{Node: n, NumLinks: st.NumLinks, Stale: st.Stale, StaleErr: st.Err, Updated: st.Updated}
			if in := s.info(n); in != nil {
				ln.Callsign = in.Callsign
			}
			for _, l := range st.Links {
				mode := l.Mode.String()
				if l.State == rpt.StateConnecting {
					mode = "C"
				}
				ln.Links = append(ln.Links, diag.Link{Node: l.Node, Mode: mode, State: l.State, Direction: string(l.Direction), Kind: l.Kind.String()})
			}
			rep.Nodes = append(rep.Nodes, ln)
		}
	}
	if s.d.Nodelist != nil {
		ns := s.d.Nodelist.Status()
		rep.NodelistCount, rep.NodelistSource, rep.NodelistUpdated = ns.Count, ns.Source, ns.Updated
		if ns.Error != nil {
			rep.NodelistError = *ns.Error
		}
	}
	if s.d.Updates != nil {
		in := s.d.Updates.Info(ctx)
		rep.UpdatesAvailable, rep.UpdatesInstalled, rep.UpdatesCandidate = in.Available, in.Installed, in.Candidate
		rep.UpdatesJob, rep.UpdatesJobLog = in.Job, in.JobLog
		if in.Reason != nil {
			rep.UpdatesReason = *in.Reason
		}
		if in.Checked != nil {
			rep.UpdatesChecked = *in.Checked
		}
	} else {
		rep.UpdatesReason = "not installed from a package"
	}
	if s.d.Firewall != nil {
		if fw, err := s.d.Firewall(ctx); err != nil {
			rep.HelperErr = err.Error()
		} else {
			rep.HaveFirewall, rep.FirewallPresent, rep.FirewallRunning, rep.FirewallEnabled = true, fw.Present, fw.Running, fw.ServiceEnabled
		}
	}
	rep.Config = configLines(s.d.Config, s.d.ConfigPath)
	if s.d.Log != nil {
		rep.Log = s.d.Log.Lines(200)
	}
	return rep
}

// configLines lists the active lines of the config file (secret values
// masked) followed by the effective configuration.
func configLines(c config.Config, path string) []string {
	var out []string
	if path != "" {
		if f, err := os.Open(path); err == nil {
			out = append(out, "# "+path+" (active lines)")
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				t := strings.TrimSpace(sc.Text())
				if t == "" || strings.HasPrefix(t, ";") || strings.HasPrefix(t, "#") {
					continue
				}
				out = append(out, diag.RedactConfigLine(sc.Text()))
			}
			f.Close()
		} else {
			out = append(out, "# "+path+": not readable")
		}
	}
	sec := func(v string) string {
		if v == "auto" || v == "" {
			return v
		}
		return diag.Redacted
	}
	out = append(out, "# effective",
		"[server]", "listen = "+c.Listen, "datadir = "+c.DataDir,
		"[ami]", "host = "+c.AMIHost, fmt.Sprintf("port = %d", c.AMIPort), "username = "+c.AMIUsername, "secret = "+sec(c.AMISecret),
		"[nodelist]", "url = "+c.NodelistURL, "refresh = "+c.NodelistRefresh.String(),
		"[helper]", "socket = "+c.HelperSocket,
		"[updates]", "url = "+c.UpdatesURL)
	return out
}
