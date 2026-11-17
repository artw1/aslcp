// Package public implements the public, read-only status view: unauthenticated
// JSON and Server-Sent Events endpoints on the main server (enabled by an admin
// setting, 404 otherwise). The data they expose is built from dedicated structs
// that can never carry IP addresses or system information, and abuse limits
// (per-IP rate limit, SSE connection caps) apply.
package public

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/artw1/aslcp/internal/store"
)

// Setting keys in the settings table.
const (
	keyEnabled   = "public.enabled"
	keyTitle     = "public.title"
	keyNodes     = "public.nodes"
	keyCallsigns = "public.callsigns"
	keyNetwork   = "public.network"
	keyActivity  = "public.activity"
	keyEmbed     = "public.embed"
	keyTrustProx = "public.trust_proxy"
)

// MaxTitle is the maximum length of the page title in runes.
const MaxTitle = 80

// Settings configure the public page. The zero value is not useful; use
// DefaultSettings.
type Settings struct {
	Enabled bool
	// Title is the page title; "" selects the automatic title.
	Title string
	// Nodes are the local nodes to show; empty means all of them.
	Nodes          []string
	ShowCallsigns  bool
	ShowNetwork    bool
	ShowActivity   bool
	AllowEmbedding bool
	// TrustProxy: take the client address from X-Forwarded-For (only for use
	// behind a reverse proxy or tunnel that sets it).
	TrustProxy bool
}

// DefaultSettings returns the defaults: off, everything visible
// except embedding.
func DefaultSettings() Settings {
	return Settings{ShowCallsigns: true, ShowNetwork: true, ShowActivity: true}
}

// SettingsStore is the part of the database the settings need.
type SettingsStore interface {
	GetSetting(k string) (string, error)
	SetSetting(k, v string) error
}

// LoadSettings reads the settings; missing or unreadable values fall back to
// the defaults (defaultEnabled applies to "enabled" when it was never set).
func LoadSettings(st SettingsStore, defaultEnabled bool) Settings {
	s := DefaultSettings()
	s.Enabled = defaultEnabled
	if st == nil {
		return s
	}
	get := func(k string) (string, bool) {
		v, err := st.GetSetting(k)
		return v, err == nil
	}
	boolv := func(k string, dst *bool) {
		if v, ok := get(k); ok {
			*dst = v == "true"
		}
	}
	boolv(keyEnabled, &s.Enabled)
	boolv(keyCallsigns, &s.ShowCallsigns)
	boolv(keyNetwork, &s.ShowNetwork)
	boolv(keyActivity, &s.ShowActivity)
	boolv(keyEmbed, &s.AllowEmbedding)
	boolv(keyTrustProx, &s.TrustProxy)
	if v, ok := get(keyTitle); ok {
		s.Title = v
	}
	if v, ok := get(keyNodes); ok && v != "" {
		for _, n := range strings.Split(v, ",") {
			if n = strings.TrimSpace(n); n != "" {
				s.Nodes = append(s.Nodes, n)
			}
		}
	}
	return s
}

// SaveSettings stores all settings.
func SaveSettings(st SettingsStore, s Settings) error {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	for _, kv := range [][2]string{
		{keyEnabled, b(s.Enabled)}, {keyTitle, s.Title},
		{keyNodes, strings.Join(s.Nodes, ",")}, {keyCallsigns, b(s.ShowCallsigns)},
		{keyNetwork, b(s.ShowNetwork)}, {keyActivity, b(s.ShowActivity)},
		{keyEmbed, b(s.AllowEmbedding)}, {keyTrustProx, b(s.TrustProxy)},
	} {
		if err := st.SetSetting(kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// ValidNode reports whether s looks like a node number.
func ValidNode(s string) bool {
	if len(s) < 1 || len(s) > 10 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Validate checks the settings.
func (s Settings) Validate() error {
	if n := len([]rune(s.Title)); n > MaxTitle {
		return fmt.Errorf("title is too long (%d characters, at most %d)", n, MaxTitle)
	}
	for _, r := range s.Title {
		if unicode.IsControl(r) {
			return errors.New("title contains control characters")
		}
	}
	if len(s.Nodes) > 64 {
		return errors.New("too many nodes selected")
	}
	for _, n := range s.Nodes {
		if !ValidNode(n) {
			return fmt.Errorf("invalid node number %q", n)
		}
	}
	return nil
}

var _ SettingsStore = (*store.Store)(nil)
