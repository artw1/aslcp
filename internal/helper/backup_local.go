package helper

import (
	"context"
	"database/sql"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LocalBackup serves the backup operations from an in-process Ops (demo mode
// and tests) with the same shape as the socket Client.
type LocalBackup struct{ Ops *Ops }

// BackupInfo implements the backup service's helper interface.
func (l LocalBackup) BackupInfo(ctx context.Context) (BackupInfo, error) {
	return l.Ops.BackupInfo(ctx)
}

// BackupCreate runs backup-create into a pipe and returns the parsed stream.
func (l LocalBackup) BackupCreate(ctx context.Context, req BackupRequest) (*BackupStream, error) {
	pr, pw := io.Pipe()
	go func() {
		_, err := l.Ops.BackupCreate(ctx, &req, pw)
		_ = pw.CloseWithError(err)
	}()
	return ReadBackupStream(pr, pr)
}

// BackupRestore implements the backup service's helper interface.
func (l LocalBackup) BackupRestore(ctx context.Context, req RestoreRequest, archive io.Reader) (RestoreResult, error) {
	return l.Ops.BackupRestore(ctx, &req, archive)
}

// demoRunner answers the package manager and service commands of a made-up
// node; git runs for real (the config history), everything else is a no-op.
type demoRunner struct{}

func (demoRunner) Run(ctx context.Context, argv ...string) (string, int, error) {
	if len(argv) > 0 && argv[0] == "env" {
		return ExecRunner{}.Run(ctx, argv...)
	}
	switch strings.Join(argv, " ") {
	case "dpkg --print-architecture":
		return "amd64\n", 0, nil
	}
	switch {
	case hasPrefix(argv, "dpkg-query", "-W", "-f=${Version}"):
		return "2.0.0", 0, nil
	case hasPrefix(argv, "dpkg-query", "-W", "-f=${Package}\\t"):
		return "asl3-asterisk-config\t3.5.0\tii \nasl3\t3.5.0\tii \nasterisk-core\t20.9\tii \n", 0, nil
	case hasPrefix(argv, "dpkg-query", "-W", "-f=${Package} ${Version}"):
		return "asl3-asterisk-config 3.5.0\n", 0, nil
	}
	return "", 0, nil
}

func hasPrefix(argv []string, p ...string) bool {
	if len(argv) < len(p) {
		return false
	}
	for i := range p {
		if i == len(p)-1 && strings.HasPrefix(argv[i], p[i]) {
			continue
		}
		if argv[i] != p[i] {
			return false
		}
	}
	return true
}

// NewDemoOps builds an Ops whose roots are all under dir, filled with the
// given /etc/asterisk files and a few made-up extras, so that backup and
// restore can be tried without touching the host.
func NewDemoOps(dir string, files map[string]string) (*Ops, error) {
	o := &Ops{
		Run:        demoRunner{},
		ConfigRoot: filepath.Join(dir, "etc-asterisk"),
		HistoryDir: filepath.Join(dir, "var", "history"),
		AstDBPath:  filepath.Join(dir, "lib", "astdb.sqlite3"),
		SoundsDirs: [2]string{filepath.Join(dir, "lib", "sounds-custom"), filepath.Join(dir, "share", "sounds-custom")},
		SA818Conf:  filepath.Join(dir, "etc", "sa818.conf"),
		AllmonDir:  filepath.Join(dir, "etc", "allmon3"),
		AslcpConf:  filepath.Join(dir, "etc", "aslcp", "aslcp.conf"),
		SourceList: filepath.Join(dir, "etc", "aslcp.list"),
		TempDir:    dir,
		NewFileOwner: func() (int, int, fs.FileMode) {
			return os.Getuid(), os.Getgid(), 0o640
		},
	}
	put := func(p, c string) error {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(c), 0o644)
	}
	for p, c := range files {
		if err := put(filepath.Join(o.ConfigRoot, p), c); err != nil {
			return nil, err
		}
	}
	extras := map[string]string{
		o.SA818Conf: "CURRENT_BAND=VHF\nCURRENT_FREQ_RX=146.5200\nCURRENT_FREQ_TX=146.5200\nCURRENT_CTCSS_RX=0023\nCURRENT_CTCSS_TX=0023\nCURRENT_DCS_RX=000\nCURRENT_DCS_TX=000\nCURRENT_PORT=/dev/ttyUSB0\n",
		filepath.Join(o.AllmonDir, "allmon3.ini"):      "[1999]\nip=127.0.0.1\n",
		filepath.Join(o.SoundsDirs[0], "welcome.ulaw"): "demo-sound",
		o.AslcpConf:  "listen = \"127.0.0.1:8080\"\n",
		o.SourceList: "deb [signed-by=/etc/apt/keyrings/aslcp.gpg] https://example.invalid/apt stable main\n",
	}
	for p, c := range extras {
		if err := put(p, c); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", o.AstDBPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE astdb (key VARCHAR(256), value VARCHAR(256))"); err != nil {
		return nil, err
	}
	if _, err := db.Exec("INSERT INTO astdb VALUES ('/demo/key', 'v')"); err != nil {
		return nil, err
	}
	return o, nil
}
