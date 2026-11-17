package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func fileEntry(path, content string) Entry {
	return Entry{Path: path, Type: TypeFile, Size: int64(len(content)), SHA256: sum(content), Owner: "root", Group: "root", Mode: "0644"}
}

func dirEntry(path string) Entry {
	return Entry{Path: path, Type: TypeDir, Owner: "root", Group: "root", Mode: "0755"}
}

func baseManifest(files ...Entry) *Manifest {
	return &Manifest{Format: 1, Created: time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC), CreatedBy: "admin",
		ASLCPVersion: "2.0.0", Hostname: "node", Timezone: "UTC", Arch: "arm64", Nodes: []string{"1999"},
		Packages: []Package{}, MissingStock: []MissingStock{}, Parts: Parts{Sounds: []string{}}, Files: files}
}

type rawEnt struct {
	hdr  tar.Header
	body string
}

// rawArchive writes a gzip tar with the manifest first and then ents,
// bypassing the Writer's checks.
func rawArchive(t *testing.T, m any, ents ...rawEnt) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	mj, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	all := append([]rawEnt{{hdr: tar.Header{Name: ManifestName, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(mj))}, body: string(mj)}}, ents...)
	for _, e := range all {
		h := e.hdr
		if h.Typeflag == tar.TypeReg || h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
			if h.Size == 0 {
				h.Size = int64(len(e.body))
			}
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			body := e.body
			if int64(len(body)) > h.Size {
				body = body[:h.Size]
			}
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func reg(path, content string) rawEnt {
	return rawEnt{hdr: tar.Header{Name: path, Mode: 0o644, Size: int64(len(content))}, body: content}
}

func TestWriterReaderRoundTrip(t *testing.T) {
	m := baseManifest(dirEntry("asterisk/etc"), fileEntry("asterisk/etc/rpt.conf", "hello\n"),
		Entry{Path: "asterisk/etc/link", Type: TypeSymlink, Target: "rpt.conf", Owner: "root", Group: "root", Mode: "0777"})
	var buf bytes.Buffer
	w, err := NewWriter(&buf, m)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Files {
		var body io.Reader
		if e.Type == TypeFile {
			body = strings.NewReader("hello\n")
		}
		if err := w.Add(e.Path, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	m2, err := Read(&buf, func(e Entry, body io.Reader) error {
		if body != nil {
			b, _ := io.ReadAll(body)
			got[e.Path] = string(b)
		}
		return nil
	})
	if err != nil || m2.Hostname != "node" || got["asterisk/etc/rpt.conf"] != "hello\n" || len(m2.Files) != 3 {
		t.Fatalf("%+v %v %v", m2, got, err)
	}
}

func TestReadRejects(t *testing.T) {
	good := fileEntry("asterisk/etc/a.conf", "x")
	etc := dirEntry("asterisk/etc")
	longName := "asterisk/etc/" + strings.Repeat("a", 2000)
	cases := []struct {
		name string
		arc  []byte
	}{
		{"traversal", rawArchive(t, baseManifest(etc, fileEntry("asterisk/etc/../../evil", "x")), reg("asterisk/etc/../../evil", "x"))},
		{"absolute", rawArchive(t, baseManifest(fileEntry("/etc/passwd", "x")), reg("/etc/passwd", "x"))},
		{"outside layout", rawArchive(t, baseManifest(fileEntry("usr/bin/evil", "x")), reg("usr/bin/evil", "x"))},
		{"dotdot component", rawArchive(t, baseManifest(etc, fileEntry("asterisk/etc/a/../b", "x")), reg("asterisk/etc/a/../b", "x"))},
		{"backslash", rawArchive(t, baseManifest(etc, fileEntry(`asterisk/etc/a\b`, "x")), reg(`asterisk/etc/a\b`, "x"))},
		{"long name", rawArchive(t, baseManifest(etc, fileEntry(longName, "x")), reg(longName, "x"))},
		{"symlink escape", rawArchive(t,
			baseManifest(etc, Entry{Path: "asterisk/etc/l", Type: TypeSymlink, Target: "../../../etc/passwd", Owner: "root", Group: "root", Mode: "0777"}),
			rawEnt{hdr: tar.Header{Name: "asterisk/etc/l", Typeflag: tar.TypeSymlink, Linkname: "../../../etc/passwd"}})},
		{"symlink absolute", rawArchive(t,
			baseManifest(etc, Entry{Path: "asterisk/etc/l", Type: TypeSymlink, Target: "/etc/passwd", Owner: "root", Group: "root", Mode: "0777"}),
			rawEnt{hdr: tar.Header{Name: "asterisk/etc/l", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}})},
		{"through symlink", rawArchive(t,
			baseManifest(etc, Entry{Path: "asterisk/etc/d", Type: TypeSymlink, Target: ".", Owner: "root", Group: "root", Mode: "0777"}, fileEntry("asterisk/etc/d/x", "x")),
			rawEnt{hdr: tar.Header{Name: "asterisk/etc/d", Typeflag: tar.TypeSymlink, Linkname: "."}}, reg("asterisk/etc/d/x", "x"))},
		{"device", rawArchive(t, baseManifest(etc, good),
			rawEnt{hdr: tar.Header{Name: "asterisk/etc/a.conf", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3}})},
		{"fifo", rawArchive(t, baseManifest(etc, good), rawEnt{hdr: tar.Header{Name: "asterisk/etc/a.conf", Typeflag: tar.TypeFifo}})},
		{"hardlink", rawArchive(t, baseManifest(etc, good), rawEnt{hdr: tar.Header{Name: "asterisk/etc/a.conf", Typeflag: tar.TypeLink, Linkname: "/etc/shadow"}})},
		{"sha mismatch", rawArchive(t, baseManifest(etc, good), reg("asterisk/etc/a.conf", "y"))},
		{"size larger than manifest", rawArchive(t, baseManifest(etc, good), reg("asterisk/etc/a.conf", strings.Repeat("x", 100)))},
		{"not in manifest", rawArchive(t, baseManifest(etc, good), reg("asterisk/etc/a.conf", "x"), reg("asterisk/etc/extra", "x"))},
		{"missing entry", rawArchive(t, baseManifest(etc, good))},
		{"duplicate path", rawArchive(t, baseManifest(etc, good, good), reg("asterisk/etc/a.conf", "x"))},
		{"bad owner name", rawArchive(t, baseManifest(etc, Entry{Path: "asterisk/etc/a.conf", Type: TypeFile, Size: 1, SHA256: sum("x"), Owner: "ro ot;", Group: "root", Mode: "0644"}), reg("asterisk/etc/a.conf", "x"))},
		{"setuid mode", rawArchive(t, baseManifest(etc, Entry{Path: "asterisk/etc/a.conf", Type: TypeFile, Size: 1, SHA256: sum("x"), Owner: "root", Group: "root", Mode: "4755"}), reg("asterisk/etc/a.conf", "x"))},
		{"no manifest first", func() []byte {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			_ = tw.WriteHeader(&tar.Header{Name: "asterisk/etc/a.conf", Typeflag: tar.TypeReg, Size: 1})
			_, _ = tw.Write([]byte("x"))
			_ = tw.Close()
			_ = gz.Close()
			return buf.Bytes()
		}()},
		{"not gzip", []byte("hello world, not a backup")},
		{"empty", nil},
	}
	for _, c := range cases {
		_, err := Read(bytes.NewReader(c.arc), nil)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		var be *Error
		var nf *NewerFormatError
		if !errors.As(err, &be) && !errors.As(err, &nf) {
			t.Errorf("%s: error %v is not a user-facing backup error", c.name, err)
		}
	}
}

func TestReadTruncated(t *testing.T) {
	m := baseManifest(dirEntry("asterisk/etc"), fileEntry("asterisk/etc/a.conf", strings.Repeat("abcdefgh", 4000)))
	arc := rawArchive(t, m, reg("asterisk/etc/a.conf", strings.Repeat("abcdefgh", 4000)))
	for _, n := range []int{len(arc) - 1, len(arc) / 2, 30} {
		if _, err := Read(bytes.NewReader(arc[:n]), nil); err == nil {
			t.Errorf("truncated at %d accepted", n)
		}
	}
}

func TestReadNewerFormat(t *testing.T) {
	m := baseManifest()
	m.Format = 2
	_, err := Read(bytes.NewReader(rawArchive(t, m)), nil)
	var nf *NewerFormatError
	if !errors.As(err, &nf) || nf.Version != 2 {
		t.Fatalf("%v", err)
	}
}

func TestReadDecompressionBomb(t *testing.T) {
	// The manifest promises 1 byte but the entry carries far more; the
	// reader must stop at the declared size, not expand it.
	m := baseManifest(dirEntry("asterisk/etc"), fileEntry("asterisk/etc/a.conf", "x"))
	big := strings.Repeat("\x00", 8<<20)
	arc := rawArchive(t, m, rawEnt{hdr: tar.Header{Name: "asterisk/etc/a.conf", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(big))}, body: big})
	if _, err := Read(bytes.NewReader(arc), nil); err == nil {
		t.Fatal("accepted")
	}
}

func TestCheckSymlinkAndPaths(t *testing.T) {
	ok := [][2]string{{"asterisk/etc/a/l", "../b"}, {"asterisk/etc/l", "x.conf"}}
	for _, c := range ok {
		if err := CheckSymlink(c[0], c[1]); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}
	bad := [][2]string{{"asterisk/etc/l", "../x"}, {"asterisk/etc/l", "/x"}, {"asterisk/etc/l", "a/../b"}, {"asterisk/etc/l", ""}, {"nowhere/l", "x"}, {"asterisk/etc/a/b/l", "../../../x"}}
	for _, c := range bad {
		if err := CheckSymlink(c[0], c[1]); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
	for _, p := range []string{"", "/a", "a/", "a//b", "a/./b", "a/../b", "a\x00b", "asterisk/etc/‮x"} {
		if ValidPath(p) == nil {
			t.Errorf("path %q accepted", p)
		}
	}
}

func TestVersionNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"2.1.0", "2.0.9", true}, {"2.0.0", "2.0.0", false}, {"1.9.9", "2.0.0", false}, {"2.0.0~beta1", "2.0.0", false}, {"", "1.0", false}, {"dev", "1.0", false}, {"1.0", "dev", false}} {
		if got := VersionNewer(c.a, c.b); got != c.want {
			t.Errorf("VersionNewer(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

// A handler that copies the body (as the services do) fails with the raw
// gzip error on a truncated upload; Read must still report a bad backup.
func TestReadTruncatedWithHandlerIsBadBackup(t *testing.T) {
	rb := make([]byte, 100000)
	rand.New(rand.NewSource(1)).Read(rb) // incompressible, so cutting the gzip lands inside the file
	body := string(rb)
	m := baseManifest(dirEntry("asterisk/etc"), fileEntry("asterisk/etc/a.conf", body))
	arc := rawArchive(t, m, reg("asterisk/etc/a.conf", body))
	h := func(e Entry, r io.Reader) error {
		if r == nil {
			return nil
		}
		_, err := io.Copy(io.Discard, r)
		return err
	}
	_, err := Read(bytes.NewReader(arc[:len(arc)/2]), h)
	var be *Error
	if !errors.As(err, &be) {
		t.Fatalf("want *Error, got %T %v", err, err)
	}
}

func TestDVSwitchPartLayout(t *testing.T) {
	ini := "dvswitch/opt/MMDVM_Bridge/MMDVM_Bridge.ini"
	m := baseManifest(fileEntry(ini, "[General]\n"))
	m.Parts.DVSwitch = true
	arc := rawArchive(t, m, reg(ini, "[General]\n"))
	m2, err := Read(bytes.NewReader(arc), nil)
	if err != nil || !m2.Parts.DVSwitch {
		t.Fatalf("%+v %v", m2, err)
	}
	// only the fixed files are valid: anything else below dvswitch/ is refused
	for _, p := range []string{"dvswitch/opt/MMDVM_Bridge/evil.sh", "dvswitch/opt/../x", "dvswitch/etc/passwd", "dvswitch/opt", "dvswitch/opt/Analog_Bridge/Analog_Bridge"} {
		bm := baseManifest(fileEntry(p, "x"))
		if err := bm.Validate(); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
	// a directory or symlink in place of a file is refused
	if err := baseManifest(dirEntry(ini)).Validate(); err == nil {
		t.Error("directory accepted for a single file")
	}
	// the flag is optional: a manifest of an older backup (no dvswitch key)
	// is still valid format 1
	var old Manifest
	raw := `{"format":1,"created":"2026-01-02T03:04:00Z","createdBy":"a","aslcpVersion":"1","asl3Version":"","asteriskVersion":"","hostname":"n","timezone":"UTC","arch":"arm64","nodes":[],"channel":"stable","packages":[],"parts":{"history":false,"allmon3":false,"sa818":false,"astdb":false,"sounds":[]},"missingStock":[],"files":[]}`
	if err := json.Unmarshal([]byte(raw), &old); err != nil || old.Validate() != nil || old.Parts.DVSwitch {
		t.Fatalf("%v %v", err, old.Validate())
	}
}
