package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/helper"
)

type fakeDVClient struct {
	status     helper.DVSwitchStatus
	installErr error
	installs   int
	jobs       []helper.DVSwitchJobs // returned in order, the last one repeats
	n          int
	jobErr     error
}

func (f *fakeDVClient) DVSwitchStatus(context.Context) (helper.DVSwitchStatus, error) {
	return f.status, nil
}
func (f *fakeDVClient) DVSwitchInstall(context.Context) (helper.DVSwitchStatus, error) {
	f.installs++
	if f.installErr != nil {
		return helper.DVSwitchStatus{}, f.installErr
	}
	return helper.DVSwitchStatus{Install: helper.DVSwitchJob{Job: helper.JobRunning}}, nil
}
func (f *fakeDVClient) DVSwitchJob(context.Context) (helper.DVSwitchJobs, error) {
	if f.jobErr != nil {
		return helper.DVSwitchJobs{}, f.jobErr
	}
	i := f.n
	if i >= len(f.jobs) {
		i = len(f.jobs) - 1
	}
	f.n++
	return f.jobs[i], nil
}

func dvRun(t *testing.T, f *fakeDVClient, root bool, args ...string) (int, string, string) {
	t.Helper()
	conf := filepath.Join(t.TempDir(), "aslcp.conf")
	if err := os.WriteFile(conf, []byte("[server]\ndatadir = "+t.TempDir()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := runDVSwitch(append(args, "--config", conf), dvEnv{
		out: &out, errw: &errb, isRoot: func() bool { return root },
		client: func(string) dvClient { return f }, sleep: func(time.Duration) {},
		poll: time.Millisecond, timeout: time.Minute,
	})
	return code, out.String(), errb.String()
}

func job(state, log string) helper.DVSwitchJobs {
	return helper.DVSwitchJobs{Install: helper.DVSwitchJob{Job: state, Log: log}}
}

func TestDVSwitchInstallSuccess(t *testing.T) {
	f := &fakeDVClient{
		status: helper.DVSwitchStatus{Supported: true, CanInstall: true, RestorePending: helper.DVRestorePending{Pending: true, BackupDate: "2026-11-20 08:15"}},
		jobs: []helper.DVSwitchJobs{
			job("running", "apt-get update\ninstalling"),
			job("running", "apt-get update\ninstalling\nsetting up"),
			job("succeeded", "installing\nsetting up\ndvswitch install finished"),
		},
	}
	code, out, errs := dvRun(t, f, true, "install")
	if code != 0 || f.installs != 1 {
		t.Fatalf("code %d installs %d: %s %s", code, f.installs, out, errs)
	}
	for _, want := range []string{"2026-11-20 08:15", "apt-get update\ninstalling\nsetting up\ndvswitch install finished\n", "DVSwitch installed."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "installing\n") != 1 {
		t.Errorf("a line was printed twice:\n%s", out)
	}
}

func TestDVSwitchInstallFailure(t *testing.T) {
	f := &fakeDVClient{status: helper.DVSwitchStatus{Supported: true, CanInstall: true},
		jobs: []helper.DVSwitchJobs{job("running", "a"), job("failed", "a\nE: boom")}}
	code, out, errs := dvRun(t, f, true, "install")
	if code != 1 || !strings.Contains(out, "E: boom") || !strings.Contains(errs, "failed") {
		t.Fatalf("code %d: %s | %s", code, out, errs)
	}
}

func TestDVSwitchInstallAlreadyInstalled(t *testing.T) {
	f := &fakeDVClient{status: helper.DVSwitchStatus{Installed: true, Supported: true}}
	code, out, _ := dvRun(t, f, true, "install")
	if code != 0 || f.installs != 0 || !strings.Contains(out, "already installed") {
		t.Fatalf("code %d installs %d: %s", code, f.installs, out)
	}
	// the helper's own refusal reads the same
	f = &fakeDVClient{status: helper.DVSwitchStatus{Supported: true, CanInstall: true}, installErr: helper.ErrDVAlreadyInstalled}
	if code, out, _ := dvRun(t, f, true, "install"); code != 0 || !strings.Contains(out, "already installed") {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestDVSwitchInstallRefusals(t *testing.T) {
	f := &fakeDVClient{status: helper.DVSwitchStatus{Supported: true, CanInstall: true}}
	if code, _, errs := dvRun(t, f, false, "install"); code != 1 || !strings.Contains(errs, "root") || f.installs != 0 {
		t.Errorf("non-root: %d %s", code, errs)
	}
	f = &fakeDVClient{status: helper.DVSwitchStatus{Supported: false, UnsupportedReason: "Debian \"buster\" is not supported"}}
	if code, _, errs := dvRun(t, f, true, "install"); code != 1 || !strings.Contains(errs, "not supported") || f.installs != 0 {
		t.Errorf("unsupported: %d %s", code, errs)
	}
	f = &fakeDVClient{status: helper.DVSwitchStatus{Supported: true, CanInstall: true}, installErr: helper.ErrDVClockUnset}
	if code, _, errs := dvRun(t, f, true, "install"); code != 1 || !strings.Contains(errs, "clock") {
		t.Errorf("clock: %d %s", code, errs)
	}
	f = &fakeDVClient{status: helper.DVSwitchStatus{Supported: true, CanInstall: true}, installErr: errors.New("x")}
	if code, _, _ := dvRun(t, f, true, "install"); code != 1 {
		t.Errorf("install error: %d", code)
	}
}

func TestDVSwitchInstallJoinsRunningJob(t *testing.T) {
	f := &fakeDVClient{status: helper.DVSwitchStatus{Supported: true, Install: helper.DVSwitchJob{Job: "running"}},
		jobs: []helper.DVSwitchJobs{job("succeeded", "done")}}
	code, out, _ := dvRun(t, f, true, "install")
	if code != 0 || f.installs != 0 || !strings.Contains(out, "already running") {
		t.Fatalf("code %d installs %d: %s", code, f.installs, out)
	}
}

func TestDVSwitchInstallLostHelper(t *testing.T) {
	f := &fakeDVClient{status: helper.DVSwitchStatus{Supported: true, CanInstall: true}, jobErr: errors.New("gone")}
	if code, _, errs := dvRun(t, f, true, "install"); code != 1 || !strings.Contains(errs, "lost the helper") {
		t.Errorf("%d %s", code, errs)
	}
}

func TestDVSwitchUsage(t *testing.T) {
	f := &fakeDVClient{}
	if code, _, errs := dvRun(t, f, true); code != 2 || !strings.Contains(errs, "Usage: aslcp dvswitch install") {
		t.Errorf("no args: %d %s", code, errs)
	}
	if code, _, _ := dvRun(t, f, true, "remove"); code != 2 {
		t.Errorf("unknown: %d", code)
	}
	if code, _, _ := dvRun(t, f, true, "install", "extra"); code != 2 {
		t.Errorf("extra arg: %d", code)
	}
	if code, out, _ := dvRun(t, f, true, "--help"); code != 0 || !strings.Contains(out, "Exit codes") {
		t.Errorf("help: %d", code)
	}
}

func TestNewLines(t *testing.T) {
	cases := []struct{ prev, cur, want []string }{
		{nil, []string{"a", "b"}, []string{"a", "b"}},
		{[]string{"a", "b"}, []string{"a", "b", "c"}, []string{"c"}},
		{[]string{"a", "b", "c"}, []string{"b", "c", "d"}, []string{"d"}}, // sliding window
		{[]string{"a"}, []string{"a"}, nil},
	}
	for i, c := range cases {
		got := newLines(c.prev, c.cur)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("case %d: %v", i, got)
		}
	}
}
