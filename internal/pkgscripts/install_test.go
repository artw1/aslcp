package pkgscripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func installScript(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// installBlocks returns the admin-setup and port-takeover blocks of install.sh.
func installBlocks(t *testing.T) string {
	t.Helper()
	s := installScript(t)
	var out string
	for _, name := range []string{"admin-setup", "port-takeover"} {
		i := strings.Index(s, "# ---8<--- "+name)
		j := strings.Index(s, "# --->8--- "+name)
		if i < 0 || j < i {
			t.Fatalf("%s markers not found", name)
		}
		out += s[i:j] + "\n"
	}
	return out
}

// tkEnv is a temporary root with a fake systemctl, ss and no firewall-cmd.
type tkEnv struct {
	dir  string
	bin  string
	conf string
}

func newTkEnv(t *testing.T, units map[string]string, listen string) *tkEnv {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	e := &tkEnv{dir: dir, bin: filepath.Join(dir, "bin"), conf: filepath.Join(dir, "aslcp.conf")}
	for _, d := range []string{e.bin, filepath.Join(dir, "sd"), filepath.Join(dir, "state"), filepath.Join(dir, "tmp")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// units: name -> "enabled|disabled|static" + ":" + "active|inactive"
	var list strings.Builder
	for u, st := range units {
		list.WriteString(u + " enabled enabled\n")
		p := strings.SplitN(st, ":", 2)
		e.write("fake/en-"+u, p[0])
		e.write("fake/ac-"+u, p[1])
	}
	e.write("fake/units", list.String())
	e.write("aslcp.conf", "[server]\nlisten = "+listen+"\n")
	fake := `#!/bin/sh
d=` + dir + `/fake
echo "$*" >>$d/calls
case "$1" in
list-unit-files) cat $d/units ;;
is-enabled) cat "$d/en-$2" 2>/dev/null || { echo not-found; exit 1; } ;;
is-active) cat "$d/ac-$2" 2>/dev/null || { echo inactive; exit 3; } ;;
disable) shift; [ "$1" = --now ] && shift; [ ! -f $d/fail-$1 ] || exit 1; echo disabled >"$d/en-$1"; echo inactive >"$d/ac-$1" ;;
enable) shift; [ "$1" = --now ] && shift; [ ! -f $d/fail-$1 ] || exit 1; echo enabled >"$d/en-$1"; echo active >"$d/ac-$1" ;;
stop) echo inactive >"$d/ac-$2" ;;
start) echo active >"$d/ac-$2" ;;
*) : ;;
esac
`
	e.writeExec("bin/systemctl", fake)
	e.writeExec("bin/ss", "#!/bin/sh\ncat "+dir+"/fake/ss 2>/dev/null\nexit 0\n")
	return e
}

func (e *tkEnv) write(rel, content string) {
	p := filepath.Join(e.dir, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		panic(err)
	}
}

func (e *tkEnv) writeExec(rel, content string) {
	e.write(rel, content)
	if err := os.Chmod(filepath.Join(e.dir, rel), 0o755); err != nil {
		panic(err)
	}
}

func (e *tkEnv) read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(e.dir, rel))
	return string(b)
}

func (e *tkEnv) run(t *testing.T, call string) (string, error) {
	t.Helper()
	script := `say() { printf '%s\n' "aslcp-install: $*"; }
die() { printf '%s\n' "aslcp-install: error: $*" >&2; exit 1; }
tmp="$ASLCP_T"
PORT_WANT="${PORT_WANT:-}"
` + installBlocks(t) + "\n" + call + "\n"
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = []string{
		"PATH=" + e.bin + ":" + os.Getenv("PATH"),
		"ASLCP_T=" + filepath.Join(e.dir, "tmp"),
		"ASLCP_CONF=" + e.conf,
		"ASLCP_SYSTEMD_DIR=" + filepath.Join(e.dir, "sd"),
		"ASLCP_DROPIN_DIR=" + filepath.Join(e.dir, "dropin"),
		"ASLCP_WEBPORT_STATE=" + filepath.Join(e.dir, "state"),
		"ASLCP_TAKEOVER_LIST=" + filepath.Join(e.dir, "lib", "takeover.list"),
		"ASLCP_DISABLED_UNITS=" + filepath.Join(e.dir, "lib", "disabled-units"),
		"PORT_WANT=" + os.Getenv("PORT_WANT_TEST"),
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestTakeoverDisableSelectsUnits(t *testing.T) {
	e := newTkEnv(t, map[string]string{
		"apache2.service":  "enabled:active",
		"allmon3.service":  "enabled:active",
		"nginx.service":    "disabled:inactive", // not enabled, not running: left alone
		"lighttpd.service": "disabled:active",   // running only: stopped
		"asterisk.service": "enabled:active",    // never touched
		"cockpit.service":  "enabled:active",    // never touched
	}, ":8080")
	out, err := e.run(t, "takeover_disable")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, want := range []string{"disabled and stopped: allmon3.service", "disabled and stopped: apache2.service", "stopped (it was running but not enabled): lighttpd.service"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	list := e.read("lib/takeover.list")
	if list != "allmon3.service enabled\napache2.service enabled\nlighttpd.service active\n" {
		t.Errorf("takeover.list = %q", list)
	}
	if rec := e.read("lib/disabled-units"); rec != "allmon3\napache2\n" {
		t.Errorf("disabled-units = %q", rec)
	}
	calls := e.read("fake/calls")
	for _, bad := range []string{"asterisk", "cockpit", "nginx.service", "aslcp"} {
		for _, l := range strings.Split(calls, "\n") {
			if (strings.HasPrefix(l, "disable") || strings.HasPrefix(l, "stop") || strings.HasPrefix(l, "enable") || strings.HasPrefix(l, "start")) && strings.Contains(l, bad) {
				t.Errorf("touched %s: %q", bad, l)
			}
		}
	}
	// Running again changes nothing and keeps the record.
	if out, err = e.run(t, "takeover_disable"); err != nil || !strings.Contains(out, "nothing was turned off") {
		t.Fatalf("second run: %v %s", err, out)
	}
	if e.read("lib/takeover.list") != list {
		t.Errorf("list changed by a second run: %q", e.read("lib/takeover.list"))
	}
}

func TestTakeoverNoUnits(t *testing.T) {
	e := newTkEnv(t, map[string]string{"asterisk.service": "enabled:active"}, ":8080")
	out, err := e.run(t, "takeover_disable")
	if err != nil || !strings.Contains(out, "nothing was turned off") {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "lib", "takeover.list")); err == nil {
		t.Error("list written although nothing was turned off")
	}
}

func TestSetPort80WritesDropinAndUndoRestores(t *testing.T) {
	e := newTkEnv(t, map[string]string{"apache2.service": "enabled:active"}, "192.0.2.10:8080")
	if out, err := e.run(t, "takeover_disable; set_port 80"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got := e.read("aslcp.conf"); !strings.Contains(got, "listen = 192.0.2.10:80\n") {
		t.Errorf("conf = %q", got)
	}
	d := e.read("dropin/port80.conf")
	for _, w := range []string{"[Service]\n", "AmbientCapabilities=CAP_NET_BIND_SERVICE\n", "CapabilityBoundingSet=CAP_NET_BIND_SERVICE\n"} {
		if !strings.Contains(d, w) {
			t.Errorf("drop-in lacks %q: %s", w, d)
		}
	}
	if p := e.read("state/previous-listen"); p != "192.0.2.10:8080\n" {
		t.Errorf("previous-listen = %q", p)
	}
	calls := e.read("fake/calls")
	if !strings.Contains(calls, "daemon-reload") || !strings.Contains(calls, "restart aslcp") {
		t.Errorf("calls: %s", calls)
	}
	if strings.Index(calls, "daemon-reload") > strings.Index(calls, "restart aslcp") {
		t.Errorf("restart before daemon-reload: %s", calls)
	}
	// Undo.
	out, err := e.run(t, "PORT_WANT=; takeover_undo")
	if err != nil {
		t.Fatalf("undo: %v: %s", err, out)
	}
	if got := e.read("aslcp.conf"); !strings.Contains(got, "listen = 192.0.2.10:8080\n") {
		t.Errorf("conf after undo = %q", got)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "dropin", "port80.conf")); err == nil {
		t.Error("drop-in still there after undo")
	}
	if e.read("fake/en-apache2.service") != "enabled\n" || e.read("fake/ac-apache2.service") != "active\n" {
		t.Error("apache2 not enabled and started again")
	}
	if _, err := os.Stat(filepath.Join(e.dir, "lib", "takeover.list")); err == nil {
		t.Error("takeover.list kept after a full undo")
	}
	if rec := e.read("lib/disabled-units"); rec != "" {
		t.Errorf("disabled-units after undo = %q", rec)
	}
	// Undo again: nothing to do.
	if out, err = e.run(t, "takeover_undo"); err != nil || !strings.Contains(out, "nothing to undo") {
		t.Fatalf("second undo: %v %s", err, out)
	}
}

func TestSetPort80RefusedWhenBusy(t *testing.T) {
	e := newTkEnv(t, nil, ":8080")
	e.write("fake/ss", "LISTEN 0 511 *:80 *:* users:((\"lighttpd\",pid=612,fd=4))\n")
	out, err := e.run(t, "set_port 80")
	if err == nil {
		t.Fatalf("expected a failing status: %s", out)
	}
	if !strings.Contains(out, `"lighttpd"`) || !strings.Contains(out, "NOT moved") || !strings.Contains(out, "pid=612") {
		t.Errorf("message: %s", out)
	}
	if got := e.read("aslcp.conf"); !strings.Contains(got, "listen = :8080\n") {
		t.Errorf("conf changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "dropin", "port80.conf")); err == nil {
		t.Error("drop-in written although the move was refused")
	}
}

func TestSetPortHighKeepsNoDropin(t *testing.T) {
	e := newTkEnv(t, nil, ":8080")
	if out, err := e.run(t, "set_port 8181"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "dropin")); err == nil {
		t.Error("drop-in directory created for a high port")
	}
	if got := e.read("aslcp.conf"); !strings.Contains(got, "listen = :8181\n") {
		t.Errorf("conf = %q", got)
	}
}

func TestUndoIgnoresUnknownEntries(t *testing.T) {
	e := newTkEnv(t, map[string]string{"apache2.service": "disabled:inactive"}, ":8080")
	e.write("lib/takeover.list", "asterisk.service enabled\nsshd.service enabled\napache2.service enabled\n")
	out, err := e.run(t, "takeover_undo")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	calls := e.read("fake/calls")
	if strings.Contains(calls, "asterisk") || strings.Contains(calls, "sshd") {
		t.Errorf("unknown units were touched: %s", calls)
	}
	if !strings.Contains(calls, "enable --now apache2.service") {
		t.Errorf("apache2 not enabled: %s", calls)
	}
}

func TestUndoKeepsFailedUnits(t *testing.T) {
	e := newTkEnv(t, map[string]string{"apache2.service": "disabled:inactive", "allmon3.service": "disabled:inactive"}, ":8080")
	e.write("lib/takeover.list", "allmon3.service enabled\napache2.service enabled\n")
	e.write("fake/fail-apache2.service", "x")
	_, err := e.run(t, "takeover_undo")
	if err == nil {
		t.Fatal("expected failure status")
	}
	if got := e.read("lib/takeover.list"); got != "apache2.service enabled\n" {
		t.Errorf("list = %q", got)
	}
}

// installHead runs the option parsing of install.sh (up to the architecture
// check) with the given environment and reports the result.
func installHead(t *testing.T, env ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	s := installScript(t)
	i := strings.Index(s, "# --- takeover (ASLCP_TAKEOVER)")
	j := strings.Index(s, "# --- architecture")
	if i < 0 || j < i {
		t.Fatal("option parsing not found")
	}
	script := `die() { printf '%s\n' "error: $*" >&2; exit 1; }
` + s[i:j] + `printf 'port=%s takeover=%s undo=%s dv=%s\n' "$PORT_WANT" "$TAKEOVER_WANT" "$TAKEOVER_UNDO" "$DV_WANT"
`
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func TestInstallOptionParsing(t *testing.T) {
	ok := []struct {
		env  []string
		want string
	}{
		{nil, "port= takeover=0 undo=0 dv=0"},
		{[]string{"ASLCP_TAKEOVER=1"}, "port=80 takeover=1 undo=0 dv=0"},
		{[]string{"ASLCP_TAKEOVER=yes"}, "port=80 takeover=1 undo=0 dv=0"},
		{[]string{"ASLCP_TAKEOVER=TRUE"}, "port=80 takeover=1 undo=0 dv=0"},
		{[]string{"ASLCP_TAKEOVER=1", "ASLCP_PORT=8181"}, "port=8181 takeover=1 undo=0 dv=0"},
		{[]string{"ASLCP_TAKEOVER=1", "ASLCP_DV=1"}, "port=80 takeover=1 undo=0 dv=1"},
		{[]string{"ASLCP_TAKEOVER=undo"}, "port= takeover=0 undo=1 dv=0"},
		{[]string{"ASLCP_TAKEOVER=undo", "ASLCP_PORT=8181"}, "port=8181 takeover=0 undo=1 dv=0"},
		{[]string{"ASLCP_PORT=80"}, "port=80 takeover=0 undo=0 dv=0"},
		{[]string{"ASLCP_PORT=1024"}, "port=1024 takeover=0 undo=0 dv=0"},
	}
	for _, c := range ok {
		got, err := installHead(t, c.env...)
		if err != nil || got != c.want {
			t.Errorf("%v: got %q (%v), want %q", c.env, got, err, c.want)
		}
	}
	bad := [][]string{
		{"ASLCP_TAKEOVER=0"},
		{"ASLCP_TAKEOVER=maybe"},
		{"ASLCP_TAKEOVER=undo", "ASLCP_PORT=80"},
		{"ASLCP_TAKEOVER=undo", "ASLCP_DV=1"},
		{"ASLCP_PORT=443"},
		{"ASLCP_PORT=81"},
		{"ASLCP_PORT=080"},
		{"ASLCP_PORT=0"},
		{"ASLCP_PORT=70000"},
		{"ASLCP_PORT=abc"},
	}
	for _, env := range bad {
		if got, err := installHead(t, env...); err == nil {
			t.Errorf("%v accepted: %q", env, got)
		}
	}
}

func TestInstallASL3OptionParsing(t *testing.T) {
	script := func(env ...string) (string, error) {
		s := installScript(t)
		i := strings.Index(s, "# --- ASL3 (ASLCP_ASL3)")
		j := strings.Index(s, "# --- architecture")
		if i < 0 || j < i {
			t.Fatal("ASL3 option parsing not found")
		}
		cmd := exec.Command("sh", "-c", `die() { printf '%s\n' "error: $*" >&2; exit 1; }
TAKEOVER_UNDO=0
`+s[i:j]+`printf '%s\n' "$ASL3_MODE"
`)
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	for env, want := range map[string]string{
		"": "auto", "auto": "auto", "1": "force", "yes": "force", "TRUE": "force",
		"0": "skip", "no": "skip", "False": "skip",
	} {
		var e []string
		if env != "" {
			e = []string{"ASLCP_ASL3=" + env}
		}
		if got, err := script(e...); err != nil || got != want {
			t.Errorf("ASLCP_ASL3=%q: got %q (%v), want %q", env, got, err, want)
		}
	}
	for _, v := range []string{"2", "maybe", "on"} {
		if got, err := script("ASLCP_ASL3=" + v); err == nil {
			t.Errorf("ASLCP_ASL3=%s accepted: %q", v, got)
		}
	}
}

// asl3Env is a temporary root with fake dpkg-query, dpkg, dpkg-deb, apt-get
// and gpg. Every call is appended to fake/calls, in order.
type asl3Env struct {
	*tkEnv
	path string // PATH for ensure; empty means fake bin plus the real PATH
}

func newASL3Env(t *testing.T, installed ...string) *asl3Env {
	t.Helper()
	e := &asl3Env{tkEnv: newTkEnv(t, nil, ":8080")}
	for _, p := range installed {
		e.write("fake/inst-"+p, "")
	}
	e.write("fake/fpr", "F2AF416869932ABB39E165E5409D50181BD84509")
	e.write("keyring.gpg", "x")
	d := filepath.Join(e.dir, "fake")
	e.writeExec("bin/dpkg-query", `#!/bin/sh
echo "dpkg-query $*" >>`+d+`/calls
for a; do p=$a; done
if [ -f `+d+`/inst-$p ]; then printf 'install ok installed'; else echo "dpkg-query: no packages found matching $p" >&2; exit 1; fi
`)
	e.writeExec("bin/dpkg", `#!/bin/sh
echo "dpkg $*" >>`+d+`/calls
case "$1" in
-i) : >`+d+`/inst-asl-apt-repos ;;
--purge) rm -f `+d+`/inst-$2 ;;
esac
`)
	e.writeExec("bin/dpkg-deb", `#!/bin/sh
echo "dpkg-deb $*" >>`+d+`/calls
cat `+d+`/debpkg 2>/dev/null || echo asl-apt-repos
`)
	e.writeExec("bin/apt-get", `#!/bin/sh
echo "apt-get $*" >>`+d+`/calls
case " $* " in
*" install "*) for a; do case "$a" in
	asl3 | asl3-menu) : >`+d+`/inst-$a ;;
	gpg) [ ! -f `+d+`/fail-gpg ] || exit 100; cp `+d+`/gpg-src `+filepath.Join(e.dir, "bin")+`/gpg ;;
esac; done ;;
esac
[ ! -f `+d+`/fail-apt ] || exit 100
`)
	e.writeExec("fake/gpg-src", `#!/bin/sh
echo "gpg $*" >>`+d+`/calls
printf 'pub:-:4096:1:409D50181BD84509:1714690500:::-:::scESC::::::23::0:\nfpr:::::::::%s:\nuid:-::::1714690500::D8C4::AllStarLink Repository:::::::::0:\nfpr:::::::::603F619811204DB98E1C7FD771EB4BABF529759F:\n' "$(cat `+d+`/fpr)"
`)
	e.writeExec("bin/gpg", e.read("fake/gpg-src"))
	return e
}

// withoutGPG removes the fake gpg and restricts PATH to the fake bin dir plus
// symlinks to the few system tools the script needs, so a real /usr/bin/gpg is
// never found. The fake apt-get installs gpg into bin when asked to.
func (e *asl3Env) withoutGPG(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(e.bin, "gpg")); err != nil {
		t.Fatal(err)
	}
	sys := filepath.Join(e.dir, "sys")
	if err := os.MkdirAll(sys, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"awk", "cat", "rm", "cp", "head", "mkdir", "dirname"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(p, filepath.Join(sys, tool)); err != nil {
			t.Fatal(err)
		}
	}
	e.path = e.bin + ":" + sys
}

// ensure runs asl3_ensure with the given mode, codename and architecture.
func (e *asl3Env) ensure(t *testing.T, mode, codename, arch string) (string, error) {
	t.Helper()
	s := installScript(t)
	i := strings.Index(s, "# ---8<--- asl3-install")
	j := strings.Index(s, "# --->8--- asl3-install")
	if i < 0 || j < i {
		t.Fatal("asl3-install markers not found")
	}
	d := filepath.Join(e.dir, "fake")
	script := `say() { printf '%s\n' "aslcp-install: $*"; }
die() { printf '%s\n' "aslcp-install: error: $*" >&2; exit 1; }
tmp="$ASLCP_T"
fetch() { echo "fetch $1" >>` + d + `/calls; : >"$2"; }
ASL3_MODE=` + mode + `
VERSION_CODENAME=` + codename + `
ARCH=` + arch + `
` + s[i:j] + "\nasl3_ensure\n"
	cmd := exec.Command("sh", "-c", script)
	path := e.path
	if path == "" {
		path = e.bin + ":" + os.Getenv("PATH")
	}
	cmd.Env = []string{
		"PATH=" + path,
		"ASLCP_T=" + filepath.Join(e.dir, "tmp"),
		"ASLCP_ASL_KEYRING=" + filepath.Join(e.dir, "keyring.gpg"),
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e *asl3Env) calls() string { return e.read("fake/calls") }

func (e *asl3Env) installs() []string {
	var r []string
	for _, l := range strings.Split(e.calls(), "\n") {
		if strings.HasPrefix(l, "apt-get ") && strings.Contains(l, " install ") {
			r = append(r, l)
		}
	}
	return r
}

func TestASL3InstallsBothWhenMissing(t *testing.T) {
	e := newASL3Env(t)
	out, err := e.ensure(t, "auto", "trixie", "arm64")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	c := e.calls()
	for _, want := range []string{
		"fetch https://repo.allstarlink.org/public/asl-apt-repos.deb13_all.deb",
		"dpkg-deb -f " + filepath.Join(e.dir, "tmp", "asl-apt-repos.deb13_all.deb") + " Package",
		"dpkg -i " + filepath.Join(e.dir, "tmp", "asl-apt-repos.deb13_all.deb"),
		"apt-get update",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("calls lack %q:\n%s", want, c)
		}
	}
	in := e.installs()
	if len(in) != 1 || !strings.HasSuffix(in[0], " asl3 asl3-menu") {
		t.Errorf("installs = %q", in)
	}
	if strings.Index(c, "dpkg -i") > strings.Index(c, "apt-get update") || strings.Index(c, "apt-get update") > strings.Index(c, "apt-get install") {
		t.Errorf("wrong order:\n%s", c)
	}
	if strings.Contains(c, "allmon3") || strings.Contains(c, "appliance") {
		t.Errorf("unexpected packages:\n%s", c)
	}
}

func TestASL3BookwormUsesDeb12(t *testing.T) {
	e := newASL3Env(t)
	if out, err := e.ensure(t, "auto", "bookworm", "amd64"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !strings.Contains(e.calls(), "asl-apt-repos.deb12_all.deb") {
		t.Errorf("calls:\n%s", e.calls())
	}
}

func TestASL3RepoAlreadyConfigured(t *testing.T) {
	e := newASL3Env(t, "asl-apt-repos")
	if out, err := e.ensure(t, "auto", "bookworm", "amd64"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if c := e.calls(); strings.Contains(c, "fetch") || strings.Contains(c, "dpkg -i") || !strings.Contains(c, "apt-get update") {
		t.Errorf("calls:\n%s", c)
	}
}

func TestASL3OnlyMenuWhenAsl3Present(t *testing.T) {
	e := newASL3Env(t, "asl3")
	out, err := e.ensure(t, "auto", "trixie", "amd64")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	in := e.installs()
	if len(in) != 1 || !strings.HasSuffix(in[0], " asl3-menu") || strings.Contains(in[0], "asl3 ") {
		t.Errorf("installs = %q", in)
	}
}

func TestASL3NothingWhenBothPresent(t *testing.T) {
	e := newASL3Env(t, "asl3", "asl3-menu")
	out, err := e.ensure(t, "auto", "trixie", "amd64")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, bad := range []string{"apt-get", "dpkg -i", "fetch"} {
		if strings.Contains(e.calls(), bad) {
			t.Errorf("unexpected %q:\n%s", bad, e.calls())
		}
	}
}

func TestASL3SkipTouchesNothing(t *testing.T) {
	e := newASL3Env(t)
	out, err := e.ensure(t, "skip", "trixie", "amd64")
	if err != nil || e.calls() != "" {
		t.Fatalf("%v: %s\ncalls: %s", err, out, e.calls())
	}
}

func TestASL3UnavailableContinuesOrFails(t *testing.T) {
	for _, c := range []struct{ codename, arch string }{{"bullseye", "amd64"}, {"", "arm64"}} {
		e := newASL3Env(t)
		out, err := e.ensure(t, "auto", c.codename, c.arch)
		if err != nil || !strings.Contains(out, "ASL3 is not available for this system") {
			t.Errorf("%v auto: %v: %s", c, err, out)
		}
		if strings.Contains(e.calls(), "apt-get") || strings.Contains(e.calls(), "fetch") {
			t.Errorf("%v: touched apt:\n%s", c, e.calls())
		}
		e = newASL3Env(t)
		out, err = e.ensure(t, "force", c.codename, c.arch)
		if err == nil || !strings.Contains(out, "ASL3 is not available for this system") {
			t.Errorf("%v force: %v: %s", c, err, out)
		}
	}
}

func TestASL3UnavailableButInstalledIsFine(t *testing.T) {
	e := newASL3Env(t, "asl3", "asl3-menu")
	if out, err := e.ensure(t, "force", "bullseye", "amd64"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestASL3FingerprintMismatchRemoves(t *testing.T) {
	e := newASL3Env(t)
	e.write("fake/fpr", "0000000000000000000000000000000000000000")
	out, err := e.ensure(t, "auto", "trixie", "amd64")
	if err == nil || !strings.Contains(out, "does not match") {
		t.Fatalf("want failure: %v: %s", err, out)
	}
	c := e.calls()
	if !strings.Contains(c, "dpkg --purge asl-apt-repos") {
		t.Errorf("package not removed:\n%s", c)
	}
	if strings.Contains(c, "apt-get") {
		t.Errorf("apt-get ran after a bad key:\n%s", c)
	}
}

func TestASL3WrongPackageRefused(t *testing.T) {
	e := newASL3Env(t)
	e.write("fake/debpkg", "something-else")
	out, err := e.ensure(t, "auto", "trixie", "amd64")
	if err == nil || strings.Contains(e.calls(), "dpkg -i") {
		t.Fatalf("want failure before dpkg -i: %v: %s\n%s", err, out, e.calls())
	}
}

func TestASL3InstallFailureIsFatal(t *testing.T) {
	e := newASL3Env(t, "asl-apt-repos")
	e.write("fake/fail-apt", "")
	if _, err := e.ensure(t, "auto", "trixie", "amd64"); err == nil {
		t.Fatal("expected failure")
	}
}

func TestASL3RunsBeforeAslcpInstall(t *testing.T) {
	s := installScript(t)
	call := strings.Index(s, "\n\tasl3_ensure\n")
	inst := strings.Index(s, "say \"installing aslcp\"")
	pre := strings.Index(s, "ASLCP_SKIP_PREFLIGHT")
	take := strings.Index(s, "takeover_disable\nfi")
	if call < 0 || inst < 0 || pre < 0 || take < 0 {
		t.Fatalf("markers missing: %d %d %d %d", call, inst, pre, take)
	}
	if !(pre < call && call < inst && inst < take) {
		t.Errorf("order wrong: preflight %d, asl3 %d, aslcp install %d, takeover %d", pre, call, inst, take)
	}
}

func TestASL3InstallsGPGBeforeRepoPackage(t *testing.T) {
	e := newASL3Env(t)
	e.withoutGPG(t)
	out, err := e.ensure(t, "auto", "trixie", "amd64")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	c := e.calls()
	g := strings.Index(c, "install -y --no-install-recommends -o DPkg::Lock::Timeout=600 gpg")
	d := strings.Index(c, "dpkg -i")
	v := strings.Index(c, "gpg --show-keys")
	if g < 0 || d < 0 || v < 0 || !(g < d && d < v) {
		t.Errorf("want gpg install, then dpkg -i, then gpg verify:\n%s", c)
	}
	if !strings.Contains(c, "apt-get update") || strings.Index(c, "apt-get update") > g {
		t.Errorf("apt-get update should precede installing gpg:\n%s", c)
	}
	if !strings.Contains(out, "repository key verified") {
		t.Errorf("key not verified: %s", out)
	}
}

func TestASL3GPGInstallFailureIsFatalAndLeavesNothing(t *testing.T) {
	e := newASL3Env(t)
	e.withoutGPG(t)
	e.write("fake/fail-gpg", "")
	out, err := e.ensure(t, "auto", "trixie", "amd64")
	if err == nil || !strings.Contains(out, "could not install gpg") {
		t.Fatalf("want failure: %v: %s", err, out)
	}
	c := e.calls()
	if strings.Contains(c, "dpkg -i") || strings.Contains(c, "fetch") {
		t.Errorf("asl-apt-repos touched after gpg failure:\n%s", c)
	}
}

// installTelemHead runs the option parsing of install.sh and prints the
// telemetry mode.
func installTelemHead(t *testing.T, env ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	s := installScript(t)
	i := strings.Index(s, "# --- takeover (ASLCP_TAKEOVER)")
	j := strings.Index(s, "# --- architecture")
	if i < 0 || j < i {
		t.Fatal("option parsing not found")
	}
	script := `die() { printf '%s\n' "error: $*" >&2; exit 1; }
` + s[i:j] + `printf 'telem=%s\n' "$TELEM_MODE"
`
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func TestInstallTelemetryOptionParsing(t *testing.T) {
	ok := map[string]string{
		"": "", "off": "off", "OFF": "off", "Off": "off", "0": "off", "no": "off", "NO": "off", "false": "off", "False": "off",
		"on": "on", "ON": "on", "1": "on", "yes": "on", "Yes": "on", "true": "on", "TRUE": "on",
	}
	for v, want := range ok {
		got, err := installTelemHead(t, "ASLCP_ASL_TELEMETRY="+v)
		if err != nil || got != "telem="+want {
			t.Errorf("%q: got %q (%v), want telem=%s", v, got, err, want)
		}
	}
	if got, err := installTelemHead(t); err != nil || got != "telem=" {
		t.Errorf("unset: got %q (%v)", got, err)
	}
	for _, v := range []string{"maybe", "2", "offf", "disable", "auto", " off"} {
		if got, err := installTelemHead(t, "ASLCP_ASL_TELEMETRY="+v); err == nil || !strings.Contains(got, "ASLCP_ASL_TELEMETRY must be") {
			t.Errorf("%q accepted or wrong error: %q (%v)", v, got, err)
		}
	}
	if got, err := installTelemHead(t, "ASLCP_ASL_TELEMETRY=off", "ASLCP_TAKEOVER=undo"); err == nil {
		t.Errorf("telemetry with takeover undo accepted: %q", got)
	}
	if got, err := installTelemHead(t, "ASLCP_ASL_TELEMETRY=off", "ASLCP_TAKEOVER=1", "ASLCP_DV=1"); err != nil || got != "telem=off" {
		t.Errorf("combined: %q (%v)", got, err)
	}
}

// telemRun runs asl_telemetry_apply with a fake systemctl. units lists the
// asl-telemetry units that exist (systemctl cat succeeds for them). failOn
// names a systemctl verb that fails. It returns the output and the calls.
func telemRun(t *testing.T, mode string, units []string, failOn string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	have := strings.Join(units, " ")
	fake := `#!/bin/sh
echo "$*" >>` + dir + `/calls
[ "$1" != "` + failOn + `" ] || exit 1
if [ "$1" = cat ]; then case " ` + have + ` " in *" $2 "*) exit 0 ;; *) exit 1 ;; esac; fi
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "bin", "systemctl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	s := installScript(t)
	i := strings.Index(s, "# ---8<--- asl-telemetry")
	j := strings.Index(s, "# --->8--- asl-telemetry")
	if i < 0 || j < i {
		t.Fatal("asl-telemetry markers not found")
	}
	script := `say() { printf '%s\n' "aslcp-install: $*"; }
set -eu
TELEM_MODE="` + mode + `"
` + s[i:j] + "\nasl_telemetry_apply\n"
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = []string{"PATH=" + filepath.Join(dir, "bin") + ":" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "calls"))
	return string(out), string(b)
}

var telemUnits = []string{"asl-telemetry.timer", "asl-telemetry.service"}

func TestTelemetryOffWithUnits(t *testing.T) {
	out, calls := telemRun(t, "off", telemUnits, "")
	for _, want := range []string{"disable --now asl-telemetry.timer\n", "mask asl-telemetry.timer asl-telemetry.service\n"} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing %q in calls:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "unmask") || strings.Contains(calls, "enable") {
		t.Errorf("unexpected calls:\n%s", calls)
	}
	if strings.Index(calls, "disable") > strings.Index(calls, "mask") {
		t.Errorf("disable must come before mask:\n%s", calls)
	}
	if !strings.Contains(out, "ASL3 telemetry turned off (asl-telemetry.timer masked)") {
		t.Errorf("output: %s", out)
	}
}

func TestTelemetryOffWithoutUnits(t *testing.T) {
	out, calls := telemRun(t, "off", nil, "")
	if strings.Contains(calls, "disable") || strings.Contains(calls, "mask") {
		t.Errorf("must not disable or mask:\n%s", calls)
	}
	if !strings.Contains(out, "not installed, nothing to turn off") {
		t.Errorf("output: %s", out)
	}
}

func TestTelemetryOffFailureIsOnlyAWarning(t *testing.T) {
	for _, verb := range []string{"disable", "mask"} {
		out, _ := telemRun(t, "off", telemUnits, verb)
		if !strings.Contains(out, "warning:") {
			t.Errorf("%s failure: no warning: %s", verb, out)
		}
	}
}

func TestTelemetryOn(t *testing.T) {
	out, calls := telemRun(t, "on", telemUnits, "")
	for _, want := range []string{"unmask asl-telemetry.timer asl-telemetry.service\n", "enable --now asl-telemetry.timer\n"} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing %q in calls:\n%s", want, calls)
		}
	}
	if strings.Index(calls, "unmask") > strings.Index(calls, "enable") {
		t.Errorf("unmask must come before enable:\n%s", calls)
	}
	if !strings.Contains(out, "ASL3 telemetry turned on") {
		t.Errorf("output: %s", out)
	}
	_, calls = telemRun(t, "on", nil, "")
	if strings.Contains(calls, "enable") || strings.Contains(calls, "unmask") {
		t.Errorf("no units: unexpected calls:\n%s", calls)
	}
}

func TestTelemetryUnsetCallsNothing(t *testing.T) {
	out, calls := telemRun(t, "", telemUnits, "")
	if calls != "" || out != "" {
		t.Errorf("calls %q, output %q", calls, out)
	}
}

func TestTelemetryRunsAfterASL3Ensure(t *testing.T) {
	s := installScript(t)
	ens := strings.Index(s, "\n\tasl3_ensure\n")
	tel := strings.Index(s, "\n\tasl_telemetry_apply\n")
	inst := strings.Index(s, "say \"installing aslcp\"")
	if ens < 0 || tel < 0 || inst < 0 || !(ens < tel && tel < inst) {
		t.Errorf("order wrong: asl3_ensure %d, telemetry %d, aslcp install %d", ens, tel, inst)
	}
	pre := strings.Index(s, "# --- pre-flight")
	parse := strings.Index(s, "# --- ASL3 telemetry (ASLCP_ASL_TELEMETRY)")
	if parse < 0 || parse > pre {
		t.Errorf("telemetry option must be parsed before the pre-flight (%d, %d)", parse, pre)
	}
}

// archCheck runs check_arch of install.sh with a fake dpkg (reporting arch)
// and uname (reporting kernel). The fake dpkg logs every call, so a test can
// see that nothing but --print-architecture was run.
func archCheck(t *testing.T, arch, kernel string) (string, string, error) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	s := installScript(t)
	i := strings.Index(s, "# ---8<--- arch-check")
	j := strings.Index(s, "# --->8--- arch-check")
	if i < 0 || j < i {
		t.Fatal("arch-check markers not found")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	fake := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fake("dpkg", "echo \"dpkg $*\" >> "+calls+"\n[ \"$1\" = --print-architecture ] && echo "+arch+" && exit 0\nexit 1\n")
	fake("uname", "echo "+kernel+"\n")
	script := `die() { printf '%s\n' "error: $*" >&2; exit 1; }
` + s[i:j] + "check_arch\necho ok\n"
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	log, _ := os.ReadFile(calls)
	return strings.TrimSpace(string(out)), string(log), err
}

func TestInstallArchAccepted(t *testing.T) {
	for _, c := range [][2]string{{"arm64", "aarch64"}, {"amd64", "x86_64"}} {
		out, _, err := archCheck(t, c[0], c[1])
		if err != nil || out != "ok" {
			t.Errorf("%s: %q (%v)", c[0], out, err)
		}
	}
}

func TestInstallArchRefusesArmhf(t *testing.T) {
	for _, kernel := range []string{"armv7l", "aarch64"} { // 32-bit userland, 32- or 64-bit kernel
		out, log, err := archCheck(t, "armhf", kernel)
		if err == nil {
			t.Fatalf("armhf on %s accepted: %q", kernel, out)
		}
		for _, want := range []string{"64-bit", "Raspberry Pi OS Lite (64-bit)", "armhf"} {
			if !strings.Contains(out, want) {
				t.Errorf("message lacks %q: %q", want, out)
			}
		}
		if log != "dpkg --print-architecture\n" {
			t.Errorf("dpkg calls = %q", log)
		}
	}
}

func TestInstallArchRefusesOthers(t *testing.T) {
	if _, _, err := archCheck(t, "i386", "i686"); err == nil {
		t.Error("i386 accepted")
	}
}

// The check runs before the pre-flight, the keyring and the apt source, so a
// refused system is left untouched.
func TestInstallArchCheckBeforeChanges(t *testing.T) {
	s := installScript(t)
	call := strings.Index(s, "check_arch\n\n# --- pre-flight")
	if call < 0 {
		t.Fatal("check_arch call not found before the pre-flight")
	}
	var code []string // the lines before the call, without comments
	for _, l := range strings.Split(s[:call], "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			code = append(code, l)
		}
	}
	before := strings.Join(code, "\n")
	for _, later := range []string{"install -m 0644 \"$tmp/keyring.gpg\"", "apt-get ", "asl3_ensure\n"} {
		if strings.Contains(before, later) {
			t.Errorf("%q comes before the architecture check", later)
		}
	}
	if k := strings.Index(s, "\n\tasl3_ensure\n"); k < call {
		t.Errorf("asl3_ensure (%d) must run after check_arch (%d)", k, call)
	}
}
