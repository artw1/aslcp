// Package config loads /etc/aslcp/aslcp.conf (Asterisk-style INI).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/astconf"
)

// Default paths.
const (
	DefaultConfigPath  = "/etc/aslcp/aslcp.conf"
	DefaultManagerConf = "/etc/asterisk/manager.conf"
)

// Config is the effective server configuration.
type Config struct {
	Listen  string
	DataDir string

	AMIHost string
	AMIPort int
	// AMIUsername and AMISecret are resolved ("auto" replaced).
	AMIUsername string
	AMISecret   string

	NodelistURL     string
	NodelistRefresh time.Duration

	// HelperSocket is the aslcp-helper unix socket ([helper] socket).
	HelperSocket string
	// UpdatesURL is the base URL of the public apt repository, which also
	// serves latest.json with release notes ([updates] url). Empty disables
	// release notes.
	UpdatesURL string

	// FavStatsURL is the base URL of the stats API used by the favorites
	// activity scan ([favstats] url); FavStatsRate its requests per minute
	// ([favstats] rate, 1..25); FavStatsLog logs one line per stats request
	// ([favstats] log, default false).
	FavStatsURL  string
	FavStatsRate int
	FavStatsLog  bool

	// ManagerConf is the path used to resolve "auto" credentials.
	ManagerConf string
}

// Defaults returns the built-in defaults.
func Defaults() Config {
	return Config{
		Listen:          ":8080",
		DataDir:         "/var/lib/aslcp",
		AMIHost:         "127.0.0.1",
		AMIPort:         5038,
		AMIUsername:     "auto",
		AMISecret:       "auto",
		NodelistURL:     "https://allmondb.allstarlink.org/",
		NodelistRefresh: 24 * time.Hour,
		ManagerConf:     DefaultManagerConf,
		HelperSocket:    "/run/aslcp/helper.sock",
		UpdatesURL:      "https://w1jar.github.io/aslcp",
		FavStatsURL:     "https://stats.allstarlink.org",
		FavStatsRate:    20,
	}
}

// AMIAddr returns host:port.
func (c Config) AMIAddr() string { return net.JoinHostPort(c.AMIHost, strconv.Itoa(c.AMIPort)) }

// ListenPort returns the TCP port of a listen address (":8080" or
// "addr:port"), validated to 1..65535.
func ListenPort(listen string) (int, error) {
	_, ps, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, fmt.Errorf("config: invalid listen address %q", listen)
	}
	p, err := strconv.Atoi(ps)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("config: invalid listen port %q (1..65535)", ps)
	}
	return p, nil
}

// Load reads path (a missing file yields the defaults) and resolves "auto"
// AMI credentials lazily via ResolveAMI.
func Load(path string) (Config, error) { return LoadWithManager(path, DefaultManagerConf) }

// LoadWithManager is Load with an explicit manager.conf path.
func LoadWithManager(path, managerConf string) (Config, error) {
	c := Defaults()
	c.ManagerConf = managerConf
	if path == "" {
		return c, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	f, err := astconf.Parse(path, data)
	if err != nil {
		return c, fmt.Errorf("config: %w", err)
	}
	get := func(sec, key string) (string, bool) {
		var val string
		ok := false
		for _, s := range f.Section(sec) {
			for _, e := range s.Get(key) {
				val, ok = strings.TrimSpace(e.Value), true
			}
		}
		return val, ok
	}
	if v, ok := get("server", "listen"); ok && v != "" {
		c.Listen = v
	}
	if v, ok := get("server", "datadir"); ok && v != "" {
		c.DataDir = v
	}
	if v, ok := get("ami", "host"); ok && v != "" {
		c.AMIHost = v
	}
	if v, ok := get("ami", "port"); ok && v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return c, fmt.Errorf("config: invalid ami port %q", v)
		}
		c.AMIPort = p
	}
	if v, ok := get("ami", "username"); ok && v != "" {
		c.AMIUsername = v
	}
	if v, ok := get("ami", "secret"); ok && v != "" {
		c.AMISecret = v
	}
	if v, ok := get("nodelist", "url"); ok {
		c.NodelistURL = v
	}
	if v, ok := get("nodelist", "refresh"); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute {
			return c, fmt.Errorf("config: invalid nodelist refresh %q", v)
		}
		c.NodelistRefresh = d
	}
	if v, ok := get("helper", "socket"); ok && v != "" {
		c.HelperSocket = v
	}
	if v, ok := get("updates", "url"); ok {
		c.UpdatesURL = strings.TrimRight(v, "/")
	}
	if v, ok := get("favstats", "url"); ok && v != "" {
		c.FavStatsURL = strings.TrimRight(v, "/")
	}
	if v, ok := get("favstats", "rate"); ok && v != "" {
		r, err := strconv.Atoi(v)
		if err != nil || r < 1 || r > 25 {
			return c, fmt.Errorf("config: invalid favstats rate %q (1..25)", v)
		}
		c.FavStatsRate = r
	}
	if v, ok := get("favstats", "log"); ok && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("config: invalid favstats log %q (true or false)", v)
		}
		c.FavStatsLog = b
	}
	return c, nil
}

// ResolveAMI replaces "auto" username/secret with the first non-general
// manager.conf section that has a secret. The secret is never logged.
func (c *Config) ResolveAMI() error {
	if c.AMIUsername != "auto" && c.AMISecret != "auto" {
		return nil
	}
	user, secret, err := ReadManagerCreds(c.ManagerConf)
	if err != nil {
		return err
	}
	if c.AMIUsername == "auto" {
		c.AMIUsername = user
	}
	if c.AMISecret == "auto" {
		c.AMISecret = secret
	}
	return nil
}

// ReadManagerCreds returns the first non-general section with a secret.
func ReadManagerCreds(path string) (user, secret string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("config: reading AMI credentials: %w", err)
	}
	f, err := astconf.Parse(path, data)
	if err != nil {
		return "", "", fmt.Errorf("config: parsing %s: %w", path, err)
	}
	for _, s := range f.Sections() {
		if s.Name == "" || strings.EqualFold(s.Name, "general") {
			continue
		}
		if e := s.Get("secret"); len(e) > 0 {
			return s.Name, strings.TrimSpace(e[len(e)-1].Value), nil
		}
	}
	return "", "", fmt.Errorf("config: no AMI user with a secret in %s", path)
}
