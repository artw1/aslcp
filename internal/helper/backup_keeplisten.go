package helper

import (
	"strings"

	"github.com/artw1/aslcp/internal/astconf"
)

// confListen returns the effective [server] listen value of an aslcp.conf
// (the last one, as config.Load reads it) and whether there is one.
func confListen(data []byte) (string, bool) {
	f, err := astconf.Parse("aslcp.conf", data)
	if err != nil {
		return "", false
	}
	var v string
	ok := false
	for _, s := range f.Section("server") {
		for _, e := range s.Get("listen") {
			if t := strings.TrimSpace(e.Value); t != "" {
				v, ok = t, true
			}
		}
	}
	return v, ok
}

// keepListen makes the restored aslcp.conf keep this node's web port: the
// [server] listen of backupConf is replaced by the one in currentConf (or
// added to [server] when the backup has none). Everything else in the backup
// file is kept byte for byte. When currentConf has no listen line or either
// file cannot be parsed, backupConf is returned unchanged. kept is the value
// taken from the node, backupListen the backup's own value ("" when it had
// none) and changed reports whether out differs from backupConf.
func keepListen(backupConf, currentConf []byte) (out []byte, kept, backupListen string, changed bool) {
	cur, ok := confListen(currentConf)
	if !ok {
		return backupConf, "", "", false
	}
	f, err := astconf.Parse("aslcp.conf", backupConf)
	if err != nil {
		return backupConf, "", "", false
	}
	backupListen, _ = confListen(backupConf)
	if backupListen == cur {
		return backupConf, cur, backupListen, false
	}
	secs := f.Section("server")
	var last *astconf.Entry
	for _, s := range secs {
		if es := s.Get("listen"); len(es) > 0 {
			last = es[len(es)-1]
		}
	}
	switch {
	case last != nil:
		last.SetValue(cur)
	case len(secs) > 0:
		if secs[0].Set("listen", cur) == nil {
			return backupConf, "", "", false
		}
	default:
		s := f.AddSection("server", false, nil)
		if s == nil || s.Set("listen", cur) == nil {
			return backupConf, "", "", false
		}
	}
	out = f.Bytes()
	return out, cur, backupListen, string(out) != string(backupConf)
}
