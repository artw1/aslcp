// Package pkgscripts tests the maintainer scripts in packaging/scripts.
package pkgscripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// migrateBlock returns the migrate-urls function of postinst.sh.
func migrateBlock(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "scripts", "postinst.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "# ---8<--- migrate-urls")
	j := strings.Index(s, "# --->8--- migrate-urls")
	if i < 0 || j < i {
		t.Fatal("migrate-urls markers not found")
	}
	return s[i:j]
}

func runMigrate(t *testing.T, list, conf string) (string, string, string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	lp, cp := filepath.Join(dir, "aslcp.list"), filepath.Join(dir, "aslcp.conf")
	if list != "" {
		os.WriteFile(lp, []byte(list), 0o644)
	}
	if conf != "" {
		os.WriteFile(cp, []byte(conf), 0o644)
	}
	script := "set -e\n" + migrateBlock(t) + "\nmigrate_urls\nmigrate_urls\n"
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "ASLCP_APT_LIST="+lp, "ASLCP_CONF="+cp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	l, _ := os.ReadFile(lp)
	c, _ := os.ReadFile(cp)
	return string(l), string(c), string(out)
}

func TestMigrateURLsList(t *testing.T) {
	const opt = "[signed-by=/usr/share/keyrings/aslcp.gpg] "
	cases := []struct{ name, in, want string }{
		{"old", "deb " + opt + "https://artw1.github.io/aslcp stable main\n", "deb " + opt + "https://w1jar.github.io/aslcp stable main\n"},
		{"older aslcp-apt", "deb " + opt + "https://artw1.github.io/aslcp-apt stable main\n", "deb " + opt + "https://w1jar.github.io/aslcp stable main\n"},
		{"trailing slash", "deb " + opt + "https://artw1.github.io/aslcp-apt/ beta main\n", "deb " + opt + "https://w1jar.github.io/aslcp/ beta main\n"},
		{"already new", "deb " + opt + "https://w1jar.github.io/aslcp stable main\n", "deb " + opt + "https://w1jar.github.io/aslcp stable main\n"},
		{"no newline at end", "deb https://artw1.github.io/aslcp", "deb https://w1jar.github.io/aslcp"},
		{"other name untouched", "deb https://artw1.github.io/aslcp-other stable main\n", "deb https://artw1.github.io/aslcp-other stable main\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, out := runMigrate(t, tc.in, "")
			if got != tc.want {
				t.Errorf("list = %q, want %q", got, tc.want)
			}
			if changed := tc.in != tc.want; changed != strings.Contains(out, "apt source") {
				t.Errorf("note printed = %v, want %v (out %q)", !changed, changed, out)
			}
			if strings.Count(out, "apt source") > 1 {
				t.Errorf("second run was not a no-op: %q", out)
			}
		})
	}
}

func TestMigrateURLsConf(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"old url", "[updates]\nurl = https://artw1.github.io/aslcp\n", "[updates]\nurl = https://w1jar.github.io/aslcp\n"},
		{"older url slash", "[updates]\nurl=https://artw1.github.io/aslcp-apt/\n", "[updates]\nurl=https://w1jar.github.io/aslcp\n"},
		{"commented stays", "[updates]\n;url = https://artw1.github.io/aslcp\n", "[updates]\n;url = https://artw1.github.io/aslcp\n"},
		{"custom url stays", "[updates]\nurl = https://example.invalid/aslcp\n", "[updates]\nurl = https://example.invalid/aslcp\n"},
		{"already new", "[updates]\nurl = https://w1jar.github.io/aslcp\n", "[updates]\nurl = https://w1jar.github.io/aslcp\n"},
		{"no url", "[web]\nlisten = :8080\n", "[web]\nlisten = :8080\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got, _ := runMigrate(t, "", tc.in)
			if got != tc.want {
				t.Errorf("conf = %q, want %q", got, tc.want)
			}
		})
	}
}
