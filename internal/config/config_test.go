package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsWhenMissing(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.DataDir != "/var/lib/aslcp" || c.AMIPort != 5038 || c.NodelistRefresh != 24*time.Hour {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestParseAndAuto(t *testing.T) {
	dir := t.TempDir()
	mgr := write(t, dir, "manager.conf", "[general]\nenabled = yes\n\n[admin]\nsecret = hunter2 ; c\nread = all\n\n[other]\nsecret = zzz\n")
	conf := write(t, dir, "aslcp.conf", "[server]\nlisten = 127.0.0.1:9999\ndatadir = /x\n[ami]\nhost = 10.0.0.1\nport = 5039\nusername = auto\nsecret = auto\n[nodelist]\nurl = http://example/\nrefresh = 2h\n")
	c, err := LoadWithManager(conf, mgr)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9999" || c.AMIAddr() != "10.0.0.1:5039" || c.NodelistRefresh != 2*time.Hour {
		t.Fatalf("%+v", c)
	}
	if err := c.ResolveAMI(); err != nil {
		t.Fatal(err)
	}
	if c.AMIUsername != "admin" || c.AMISecret != "hunter2" {
		t.Fatalf("auto creds: %q %q", c.AMIUsername, c.AMISecret)
	}
}

func TestExplicitCredsNotOverridden(t *testing.T) {
	c := Defaults()
	c.AMIUsername, c.AMISecret = "u", "s"
	c.ManagerConf = "/nonexistent"
	if err := c.ResolveAMI(); err != nil || c.AMIUsername != "u" {
		t.Fatal(err, c)
	}
}

func TestAutoMissingFileErrorHasNoSecret(t *testing.T) {
	c := Defaults()
	c.ManagerConf = "/nonexistent/manager.conf"
	err := c.ResolveAMI()
	if err == nil || strings.Contains(err.Error(), "secret =") {
		t.Fatal(err)
	}
}

func TestBadValues(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(write(t, dir, "a.conf", "[ami]\nport = abc\n")); err == nil {
		t.Fatal("want error")
	}
	if _, err := Load(write(t, dir, "b.conf", "[nodelist]\nrefresh = 1s\n")); err == nil {
		t.Fatal("want error")
	}
}

func TestHelperAndUpdatesSections(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(filepath.Join(dir, "none.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if c.HelperSocket != "/run/aslcp/helper.sock" || c.UpdatesURL != "https://artw1.github.io/aslcp" {
		t.Fatalf("defaults: %+v", c)
	}
	conf := write(t, dir, "aslcp.conf", "[helper]\nsocket = /tmp/h.sock\n[updates]\nurl = https://example.org/apt/\n")
	if c, err = Load(conf); err != nil {
		t.Fatal(err)
	}
	if c.HelperSocket != "/tmp/h.sock" || c.UpdatesURL != "https://example.org/apt" {
		t.Fatalf("%+v", c)
	}
	conf = write(t, dir, "off.conf", "[updates]\nurl =\n")
	if c, _ = Load(conf); c.UpdatesURL != "" {
		t.Fatalf("empty url should disable notes: %+v", c)
	}
}

func TestFavStatsSection(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(write(t, dir, "a.conf", ""))
	if err != nil || c.FavStatsURL != "https://stats.allstarlink.org" || c.FavStatsRate != 20 || c.FavStatsLog {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c, err = Load(write(t, dir, "b.conf", "[favstats]\nurl = http://example.invalid/\nrate = 12\nlog = true\n"))
	if err != nil || c.FavStatsURL != "http://example.invalid" || c.FavStatsRate != 12 || !c.FavStatsLog {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := Load(write(t, dir, "d.conf", "[favstats]\nlog = maybe\n")); err == nil {
		t.Error("bad log value accepted")
	}
	for _, bad := range []string{"0", "26", "x"} {
		if _, err := Load(write(t, dir, "c.conf", "[favstats]\nrate = "+bad+"\n")); err == nil {
			t.Errorf("rate %q accepted", bad)
		}
	}
}
