package pkgscripts

import (
	"encoding/json"
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
	if got, want := strings.TrimRight(string(out), "\n"), "https://example.invalid/lite-arm64.img.xz\t\t2000-01-03\tcloudinit-rpi\t[\"pi5-64bit\",\"pi4-64bit\",\"pi3-64bit\"]"; got != want {
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

func TestImagerJSON(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("no jq")
	}
	out := filepath.Join(t.TempDir(), "aslcp-imager.json")
	cmd := exec.Command("bash", imageScript("build-image.sh"), "--imager-json", filepath.Join("testdata", "os_list_sample.json"), out)
	cmd.Env = append(os.Environ(), "IMAGE_DATE=2026.03.04", "IMAGE_BASE_URL=https://downloads.example.invalid/dl/image-test-1")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s", raw)
	var doc struct {
		Imager struct {
			LatestVersion string `json:"latest_version"`
			Devices       []struct {
				Name, Icon   string
				MatchingType string `json:"matching_type"`
				Tags         []string
				Default      bool
			} `json:"devices"`
		} `json:"imager"`
		OSList []struct {
			Name, Description, Website, URL string
			ExtractSize                     int64    `json:"extract_size"`
			ExtractSHA                      string   `json:"extract_sha256"`
			DownloadSize                    int64    `json:"image_download_size"`
			DownloadSHA                     string   `json:"image_download_sha256"`
			ReleaseDate                     string   `json:"release_date"`
			Devices                         []string `json:"devices"`
			InitFormat                      string   `json:"init_format"`
			Icon                            string   `json:"icon"`
		} `json:"os_list"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc.Imager.LatestVersion == "" || len(doc.Imager.Devices) != 4 {
		t.Fatalf("imager block: %+v", doc.Imager)
	}
	if d := doc.Imager.Devices[0]; d.Name != "Raspberry Pi 5" || !d.Default || d.MatchingType != "exclusive" {
		t.Errorf("first device: %+v", d)
	}
	for _, d := range doc.Imager.Devices {
		if !strings.HasPrefix(d.Icon, "https://downloads.raspberrypi.com/imager/icons/") || len(d.Tags) == 0 {
			t.Errorf("device %+v", d)
		}
	}
	if len(doc.OSList) != 1 {
		t.Fatalf("os_list has %d entries", len(doc.OSList))
	}
	e := doc.OSList[0]
	if e.Name != "ASLCP for ASL3 2026.03.04" || e.Website != "https://github.com/w1jar/aslcp" || e.Icon != "" {
		t.Errorf("entry: %+v", e)
	}
	if !strings.Contains(e.Description, "nofficial") || !strings.Contains(e.Description, "Raspberry Pi OS Lite (64-bit)") || !strings.Contains(e.Description, "AllStarLink 3") {
		t.Errorf("description: %q", e.Description)
	}
	if e.URL != "https://downloads.example.invalid/dl/image-test-1/aslcp-asl3-arm64-2026.03.04.img.xz" {
		t.Errorf("url: %q", e.URL)
	}
	if e.InitFormat != "cloudinit-rpi" || strings.Join(e.Devices, ",") != "pi5-64bit,pi4-64bit,pi3-64bit" {
		t.Errorf("init_format/devices: %q %v", e.InitFormat, e.Devices)
	}
	sha := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if e.ExtractSize <= 0 || e.DownloadSize <= 0 || !sha.MatchString(e.ExtractSHA) || !sha.MatchString(e.DownloadSHA) {
		t.Errorf("sizes/checksums: %+v", e)
	}
	if e.ReleaseDate != "2026-03-04" {
		t.Errorf("release_date: %q", e.ReleaseDate)
	}
	// An os list entry without init_format/devices falls back to the defaults.
	p := filepath.Join(t.TempDir(), "l.json")
	if err := os.WriteFile(p, []byte(`{"os_list":[{"name":"Raspberry Pi OS Lite (64-bit)","url":"https://example.invalid/x.img.xz"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command("bash", imageScript("build-image.sh"), "--select-base", p).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if !strings.HasSuffix(strings.TrimRight(string(b), "\n"), "\tcloudinit-rpi\t[\"pi5-64bit\",\"pi4-64bit\",\"pi3-64bit\"]") {
		t.Errorf("fallbacks: %q", b)
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
