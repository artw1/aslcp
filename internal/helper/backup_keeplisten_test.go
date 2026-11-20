package helper

import (
	"github.com/artw1/aslcp/internal/backup"
	"strings"
	"testing"
)

func TestKeepListen(t *testing.T) {
	cases := []struct {
		name, backup, current string
		want                  string
		kept, was             string
		changed               bool
	}{
		{"port differs, comments kept",
			"; my node\n[server]\n# web port\nlisten = :8080\ndatadir = /var/lib/aslcp\n\n[ami]\nhost = 192.0.2.1\n",
			"[server]\nlisten = :8181\n",
			"; my node\n[server]\n# web port\nlisten = :8181\ndatadir = /var/lib/aslcp\n\n[ami]\nhost = 192.0.2.1\n",
			":8181", ":8080", true},
		{"backup without listen",
			"[server]\ndatadir = /var/lib/aslcp\n; end\n",
			"[server]\nlisten = :8181\n",
			"[server]\ndatadir = /var/lib/aslcp\nlisten = :8181\n; end\n",
			":8181", "", true},
		{"backup without server section",
			"[ami]\nhost = 192.0.2.1\n",
			"[server]\nlisten = :8181\n",
			"[ami]\nhost = 192.0.2.1\n\n[server]\nlisten = :8181\n",
			":8181", "", true},
		{"current without listen",
			"[server]\nlisten = :8080\n", "[server]\ndatadir = /x\n",
			"[server]\nlisten = :8080\n", "", "", false},
		{"identical",
			"[server]\nlisten = :8181 ; same\n", "[server]\nlisten=:8181\n",
			"[server]\nlisten = :8181 ; same\n", ":8181", ":8181", false},
		{"inline comment",
			"[server]\nlisten = :8080\n",
			"[server]\nlisten = 192.0.2.10:8181 ; LAN only\n",
			"[server]\nlisten = 192.0.2.10:8181\n",
			"192.0.2.10:8181", ":8080", true},
		{"backup inline comment kept",
			"[server]\nlisten = :8080 ; web\n", "[server]\nlisten = :8181\n",
			"[server]\nlisten = :8181 ; web\n", ":8181", ":8080", true},
		{"no trailing newline, CRLF-free",
			"[server]\nlisten = :8080", "[server]\nlisten = :8181\n",
			"[server]\nlisten = :8181", ":8181", ":8080", true},
		{"empty current", "[server]\nlisten = :8080\n", "", "[server]\nlisten = :8080\n", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, kept, was, changed := keepListen([]byte(c.backup), []byte(c.current))
			if string(out) != c.want || kept != c.kept || was != c.was || changed != c.changed {
				t.Errorf("got %q kept=%q was=%q changed=%v\nwant %q kept=%q was=%q changed=%v",
					out, kept, was, changed, c.want, c.kept, c.was, c.changed)
			}
		})
	}
}

func TestBackupRestoreKeepsListen(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.put("etc-asterisk/custom/x.conf", "a\n", 0o640)
	const backupConf = "# old node\n[server]\nlisten = :8080\n[ami]\nhost = 192.0.2.1\n"
	archive := src.archiveExtra(BackupRequest{}, map[string]string{backup.PathAslcpConf: backupConf})

	dst := newBkNode(t, "1.0", nil)
	dst.put("etc/aslcp/aslcp.conf", "[server]\nlisten = :8181\n", 0o664)
	res, err := restoreInto(t, dst, archive, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	want := "# old node\n[server]\nlisten = :8181\n[ami]\nhost = 192.0.2.1\n"
	if got := dst.get("etc/aslcp/aslcp.conf"); got != want {
		t.Errorf("conf %q", got)
	}
	found := false
	for _, s := range res.Steps {
		if s.Name == "Restore ASLCP settings" {
			found = true
			if !s.OK || !strings.Contains(s.Detail, "listen = :8181") || !strings.Contains(s.Detail, ":8080") {
				t.Errorf("step %+v", s)
			}
		}
	}
	if !found {
		t.Errorf("no step: %+v", res.Steps)
	}

	// no current conf: the backup's content is restored as is
	dst2 := newBkNode(t, "1.0", nil)
	if res, err := restoreInto(t, dst2, archive, RestoreRequest{}); err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if got := dst2.get("etc/aslcp/aslcp.conf"); got != backupConf {
		t.Errorf("conf %q", got)
	}
}
