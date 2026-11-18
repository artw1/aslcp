package susb

import (
	"context"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/artw1/aslcp/internal/astconf"
	"github.com/artw1/aslcp/internal/helper"
)

// ConfigSource reads configuration files through the root helper (the
// server never reads /etc/asterisk itself).
type ConfigSource interface {
	ConfigList(ctx context.Context) ([]helper.ConfigInfo, error)
	ConfigRead(ctx context.Context, path string) (helper.ConfigContent, error)
}

// Recorder commits a radio configuration file to the config history after
// Asterisk rewrote it (helper op config-record).
type Recorder interface {
	ConfigRecord(ctx context.Context, path, message, author string) (helper.ConfigApplyResult, error)
}

// SimpleUSBConf is the file "susb tune save" rewrites.
const SimpleUSBConf = "simpleusb.conf"

// helperFS adapts a ConfigSource to astconf.FS.
type helperFS struct {
	ctx context.Context
	src ConfigSource
}

func (h helperFS) rel(p string) string {
	return strings.TrimPrefix(p, helper.DefaultConfigRoot+"/")
}

func (h helperFS) ReadFile(p string) ([]byte, error) {
	c, err := h.src.ConfigRead(h.ctx, h.rel(p))
	if err != nil {
		if strings.Contains(err.Error(), helper.ErrNoSuchFile) {
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	return []byte(c.Content), nil
}

func (h helperFS) Glob(pattern string) ([]string, error) {
	list, err := h.src.ConfigList(h.ctx)
	if err != nil {
		return nil, err
	}
	pat := h.rel(pattern)
	var out []string
	for _, f := range list {
		if ok, _ := path.Match(pat, f.Path); ok {
			out = append(out, f.Path)
		}
	}
	return out, nil
}

func loadTree(ctx context.Context, src ConfigSource, file string) (*astconf.Tree, error) {
	return astconf.LoadTree(helperFS{ctx: ctx, src: src}, "", file)
}

func lastValue(es []astconf.ResolvedEntry, key string) (string, bool) {
	v, ok := "", false
	for _, e := range es {
		if strings.EqualFold(e.Key, key) {
			v, ok = strings.TrimSpace(e.Value), true
		}
	}
	return v, ok
}

var reNodeName = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

// deviceNodes maps SimpleUSB device names to node numbers using
// "rxchannel = SimpleUSB/<dev>" of the node sections of rpt.conf.
func deviceNodes(ctx context.Context, src ConfigSource) (map[string]string, error) {
	t, err := loadTree(ctx, src, "rpt.conf")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	seen := map[string]bool{}
	for _, s := range t.Sections {
		if s.Template || !reNodeName.MatchString(s.Name) || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		es, err := astconf.Effective(t, s.Name)
		if err != nil {
			continue
		}
		rx, ok := lastValue(es, "rxchannel")
		if !ok {
			continue
		}
		drv, dev, ok := strings.Cut(rx, "/")
		if !ok || !strings.EqualFold(drv, "simpleusb") {
			continue
		}
		dev, _, _ = strings.Cut(dev, "/")
		dev = strings.TrimSpace(dev)
		if dev != "" {
			if _, dup := out[dev]; !dup {
				out[dev] = s.Name
			}
		}
	}
	return out, nil
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "true", "y", "t", "1", "on":
		return true
	}
	return false
}

// Saved overlays the values stored for dev in simpleusb.conf (templates and
// includes resolved) onto base; the returned set names the fields that the
// file defines. Values that cannot be understood are left as they are.
func saved(ctx context.Context, src ConfigSource, dev string, base Params) (Params, map[string]bool, error) {
	t, err := loadTree(ctx, src, SimpleUSBConf)
	if err != nil {
		return base, nil, err
	}
	es, err := astconf.Effective(t, dev)
	if err != nil {
		return base, nil, err
	}
	return overlay(es, base)
}

func overlay(es []astconf.ResolvedEntry, base Params) (Params, map[string]bool, error) {
	have := map[string]bool{}
	p := base
	level := func(key, field string, dst *int) {
		if v, ok := lastValue(es, key); ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= MaxLevel {
				*dst, have[field] = n, true
			}
		}
	}
	flag := func(key, field string, dst *bool) {
		if v, ok := lastValue(es, key); ok {
			*dst, have[field] = isTrue(v), true
		}
	}
	delay := func(key, field string, dst *int) {
		if v, ok := lastValue(es, key); ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= MaxDelayFrames {
				*dst, have[field] = FramesToMs(n), true
			}
		}
	}
	signal := func(key, field string, codes map[string]int, dst *string, other *bool) {
		if v, ok := lastValue(es, key); ok {
			v = strings.ToLower(v)
			if _, known := codes[v]; known {
				*dst, *other, have[field] = v, false, true
			}
		}
	}
	level("rxmixerset", "rxVoice", &p.RxVoice)
	level("txmixaset", "txA", &p.TxA)
	level("txmixbset", "txB", &p.TxB)
	flag("rxboost", "rxBoost", &p.RxBoost)
	flag("preemphasis", "preEmphasis", &p.PreEmphasis)
	flag("deemphasis", "deEmphasis", &p.DeEmphasis)
	flag("plfilter", "plFilter", &p.PlFilter)
	flag("invertptt", "pttInvert", &p.PttInvert)
	signal("carrierfrom", "carrierFrom", carrierCodes, &p.CarrierFrom, &p.CarrierOther)
	signal("ctcssfrom", "ctcssFrom", ctcssCodes, &p.CtcssFrom, &p.CtcssOther)
	delay("rxondelay", "rxOnDelayMs", &p.RxOnDelayMs)
	delay("txoffdelay", "txOffDelayMs", &p.TxOffDelayMs)
	return p, have, nil
}

// USRPNode returns the first local node of rpt.conf whose rxchannel is a
// chan_usrp channel ("USRP/..."), the bridge node of DVSwitch (read-only).
func USRPNode(ctx context.Context, src ConfigSource) (string, bool) {
	t, err := loadTree(ctx, src, "rpt.conf")
	if err != nil {
		return "", false
	}
	for _, s := range t.Sections {
		if s.Template || !reNodeName.MatchString(s.Name) {
			continue
		}
		es, err := astconf.Effective(t, s.Name)
		if err != nil {
			continue
		}
		if rx, ok := lastValue(es, "rxchannel"); ok {
			if drv, _, _ := strings.Cut(rx, "/"); strings.EqualFold(strings.TrimSpace(drv), "usrp") {
				return s.Name, true
			}
		}
	}
	return "", false
}
