package pkgscripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func imageScript(name string) string {
	return filepath.Join("..", "..", "packaging", "image", name)
}

const sampleManager = `;
; Asterisk Call Management support
[general]
enabled = yes
port = 5038
bindaddr = 127.0.0.1				; Comment when not localhost access

[admin]
secret = make4An1ce-secret   ; keep private
read = all,system
write = all,system
transfer = no
`

const sampleIAX = `[general]
bindport = 4569

[radio]
type = friend
secret = Your_Secret_Password_Here

[allstar-public]
type = user
secret = allstar
`

type fbRoot struct {
	root string
}

func newFbRoot(t *testing.T) *fbRoot {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	r := &fbRoot{root: t.TempDir()}
	for _, d := range []string{"etc/asterisk", "var/lib/aslcp-image"} {
		if err := os.MkdirAll(filepath.Join(r.root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.write(t, "etc/asterisk/manager.conf", sampleManager, 0o640)
	r.write(t, "etc/asterisk/iax.conf", sampleIAX, 0o644)
	r.write(t, "var/lib/aslcp-image/firstboot-needed", "", 0o644)
	return r
}

func (r *fbRoot) write(t *testing.T, rel, s string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(r.root, rel)
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func (r *fbRoot) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *fbRoot) run(t *testing.T) string {
	t.Helper()
	log := filepath.Join(r.root, "systemctl.log")
	fake := filepath.Join(r.root, "fake-systemctl")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\" >>"+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", imageScript("aslcp-firstboot.sh"))
	cmd.Env = append(os.Environ(), "ROOT="+r.root, "SYSTEMCTL="+fake)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("firstboot: %v\n%s", err, out)
	}
	return string(out)
}

func TestFirstbootReplacesSecretsOnly(t *testing.T) {
	r := newFbRoot(t)
	out := r.run(t)

	mgr := r.read(t, "etc/asterisk/manager.conf")
	re := regexp.MustCompile(`(?m)^secret = ([0-9a-f]{16})   ; keep private$`)
	m := re.FindStringSubmatch(mgr)
	if m == nil {
		t.Fatalf("AMI secret not replaced as expected:\n%s", mgr)
	}
	// Everything but that one line is byte-identical.
	if got, want := re.ReplaceAllString(mgr, "secret = make4An1ce-secret   ; keep private"), sampleManager; got != want {
		t.Errorf("other lines of manager.conf changed:\n%s", got)
	}

	iax := r.read(t, "etc/asterisk/iax.conf")
	re2 := regexp.MustCompile(`(?m)^secret = ([0-9a-f]{32})$`)
	if m2 := re2.FindStringSubmatch(iax); m2 == nil {
		t.Fatalf("IAX placeholder not replaced:\n%s", iax)
	} else if got := strings.Replace(iax, m2[1], "Your_Secret_Password_Here", 1); got != sampleIAX {
		t.Errorf("other lines of iax.conf changed:\n%s", got)
	}
	if !strings.Contains(iax, "secret = allstar\n") {
		t.Error("the public allstar secret must stay")
	}
	if strings.Contains(out, m[1]) {
		t.Error("the script must not print secrets")
	}

	for rel, want := range map[string]os.FileMode{"etc/asterisk/manager.conf": 0o640, "etc/asterisk/iax.conf": 0o644} {
		st, err := os.Stat(filepath.Join(r.root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s mode %v, want %v", rel, st.Mode().Perm(), want)
		}
	}
	if _, err := os.Stat(filepath.Join(r.root, "var/lib/aslcp-image/firstboot-needed")); !os.IsNotExist(err) {
		t.Error("marker not removed")
	}
	if !strings.Contains(r.read(t, "systemctl.log"), "disable aslcp-firstboot.service") {
		t.Error("unit not disabled")
	}
}

func TestFirstbootIdempotent(t *testing.T) {
	r := newFbRoot(t)
	r.run(t)
	mgr, iax := r.read(t, "etc/asterisk/manager.conf"), r.read(t, "etc/asterisk/iax.conf")
	r.run(t) // marker is gone: nothing happens
	if r.read(t, "etc/asterisk/manager.conf") != mgr || r.read(t, "etc/asterisk/iax.conf") != iax {
		t.Error("second run changed the files")
	}
}

func TestFirstbootRandomPerRun(t *testing.T) {
	a, b := newFbRoot(t), newFbRoot(t)
	a.run(t)
	b.run(t)
	if a.read(t, "etc/asterisk/manager.conf") == b.read(t, "etc/asterisk/manager.conf") {
		t.Error("two cards got the same AMI secret")
	}
}

func TestFirstbootMissingFilesAreFine(t *testing.T) {
	r := newFbRoot(t)
	_ = os.Remove(filepath.Join(r.root, "etc/asterisk/iax.conf"))
	r.write(t, "etc/asterisk/manager.conf", "[general]\nenabled = yes\n", 0o640)
	r.run(t)
	if got := r.read(t, "etc/asterisk/manager.conf"); got != "[general]\nenabled = yes\n" {
		t.Errorf("file without [admin] secret was modified: %q", got)
	}
}

func TestSelectBase(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("no jq")
	}
	out, err := exec.Command("bash", imageScript("build-image.sh"), "--select-base", filepath.Join("testdata", "os_list_sample.json")).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, want := strings.TrimRight(string(out), "\n"), "https://example.invalid/lite-arm64.img.xz\t\t2000-01-03"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// No Lite (64-bit) entry: an error, not a wrong pick.
	p := filepath.Join(t.TempDir(), "l.json")
	if err := os.WriteFile(p, []byte(`{"os_list":[{"name":"Other","url":"https://example.invalid/x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", imageScript("build-image.sh"), "--select-base", p).CombinedOutput(); err == nil {
		t.Errorf("expected failure, got %q", out)
	}
}

func TestSetupLinkScript(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	root := t.TempDir()
	for _, d := range []string{"etc/aslcp", "var/lib/aslcp"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc/aslcp/aslcp.conf"), []byte("[server]\nlisten = :8181\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func() (string, string) {
		con := filepath.Join(root, "console")
		_ = os.Remove(con)
		cmd := exec.Command("sh", imageScript("aslcp-setup-link.sh"))
		cmd.Env = append(os.Environ(), "ROOT="+root, "CONSOLE="+con, "WAIT=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		b, _ := os.ReadFile(con)
		return string(out), string(b)
	}
	// No code (an administrator exists): silent.
	if out, con := run(); strings.Contains(out+con, "setup?code") {
		t.Errorf("link shown without a code: %q", out)
	}
	if err := os.WriteFile(filepath.Join(root, "var/lib/aslcp/setup-code"), []byte("abc123\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	out, con := run()
	for _, s := range []string{out, con} {
		if !strings.Contains(s, ".local:8181/#/setup?code=abc123") {
			t.Errorf("link missing in %q", s)
		}
	}
	issue := filepath.Join(root, "run/issue.d/50-aslcp-setup.issue")
	st, err := os.Stat(issue)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("issue file mode %v, want 0600", st.Mode().Perm())
	}
}
