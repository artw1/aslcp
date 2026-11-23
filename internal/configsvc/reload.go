package configsvc

import (
	"sort"
	"strings"
)

// ReloadMap maps a config file to the AMI command that applies a change to
// it. A key ending in "/" matches every file below that directory. Only these
// fixed strings are ever sent to Asterisk for a reload. Corrected after
// testing on the node (DESIGN 13, "Reload semantics").
var ReloadMap = map[string]string{
	"rpt.conf":                    "module reload app_rpt.so",
	"custom/rpt/":                 "module reload app_rpt.so",
	"iax.conf":                    "iax2 reload",
	"custom/iax/":                 "iax2 reload",
	"extensions.conf":             "dialplan reload",
	"custom/extensions/":          "dialplan reload",
	"manager.conf":                "manager reload",
	"simpleusb.conf":              "module reload chan_simpleusb.so",
	"usbradio.conf":               "module reload chan_usbradio.so",
	"echolink.conf":               "module reload chan_echolink.so",
	"rpt_http_registrations.conf": "module reload res_rpt_http_registrations.so",
	"logger.conf":                 "logger reload",
}

// ReloadStep is one planned reload.
type ReloadStep struct {
	Command string `json:"command"`
	Reason  string `json:"reason"`
}

func reloadCommand(path string) (string, bool) {
	if c, ok := ReloadMap[path]; ok && !strings.HasSuffix(path, "/") {
		return c, true
	}
	for k, c := range ReloadMap {
		if strings.HasSuffix(k, "/") && strings.HasPrefix(path, k) {
			return c, true
		}
	}
	return "", false
}

// PlanReload derives the reload commands (de-duplicated, in a stable order)
// from the changed files; restart is true when any file has no reload.
func PlanReload(changed []string) (steps []ReloadStep, restart bool) {
	paths := append([]string(nil), changed...)
	sort.Strings(paths)
	idx := map[string]int{}
	files := map[string][]string{}
	for _, p := range paths {
		c, ok := reloadCommand(p)
		if !ok {
			restart = true
			continue
		}
		if _, seen := idx[c]; !seen {
			idx[c] = len(steps)
			steps = append(steps, ReloadStep{Command: c})
		}
		files[c] = append(files[c], p)
	}
	for i := range steps {
		steps[i].Reason = strings.Join(files[steps[i].Command], ", ") + " changed"
	}
	return steps, restart
}

// ReloadCommandFor returns the reload command that applies a change to the
// file, and false when only an Asterisk restart does.
func ReloadCommandFor(path string) (string, bool) { return reloadCommand(path) }
