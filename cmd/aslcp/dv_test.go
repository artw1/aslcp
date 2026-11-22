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
	status    helper.DVStatus
	enableErr error
	enables   int
	disables  int
	jobs      []helper.DVJob // returned in order, the last one repeats
	n         int
	jobErr    error
}

func (f *fakeDVClient) DVStatus(context.Context) (helper.DVStatus, error) { return f.status, nil }
func (f *fakeDVClient) DVEnable(context.Context) (helper.DVStatus, error) {
	f.enables++
	return helper.DVStatus{Job: "running"}, f.enableErr
}
func (f *fakeDVClient) DVDisable(context.Context) (helper.DVStatus, error) {
	f.disables++
	return helper.DVStatus{}, nil
}
func (f *fakeDVClient) DVJob(context.Context) (helper.DVJob, error) {
	if f.jobErr != nil {
		return helper.DVJob{}, f.jobErr
	}
	i := min(f.n, len(f.jobs)-1)
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
	code := runDV(append(args, "--config", conf), dvEnv{
		out: &out, errw: &errb, isRoot: func() bool { return root },
		client: func(string) dvClient { return f }, sleep: func(time.Duration) {},
		poll: time.Millisecond, timeout: time.Minute,
	})
	return code, out.String(), errb.String()
}

func TestDVEnableSuccess(t *testing.T) {
	f := &fakeDVClient{
		status: helper.DVStatus{Supported: true, RestorePending: helper.DVRestorePending{Pending: true, BackupDate: "2026-11-20 08:15"}},
		jobs: []helper.DVJob{
			{Job: "running", Log: "step one\nstep two"},
			{Job: "running", Log: "step one\nstep two\nstep three"},
			{Job: "done", Log: "step two\nstep three\nfinished"},
		},
	}
	for _, cmd := range []string{"enable", "install"} {
		f.n = 0
		code, out, errs := dvRun(t, f, true, cmd)
		if code != 0 {
			t.Fatalf("%s: code %d: %s %s", cmd, code, out, errs)
		}
		for _, want := range []string{"2026-11-20 08:15", "step one\nstep two\nstep three\nfinished\n", "Digital modes enabled."} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output lacks %q:\n%s", cmd, want, out)
			}
		}
	}
	if f.enables != 2 {
		t.Errorf("enables = %d", f.enables)
	}
}

func TestDVEnableFailure(t *testing.T) {
	f := &fakeDVClient{status: helper.DVStatus{Supported: true}, jobs: []helper.DVJob{{Job: "running"}, {Job: "failed", Log: "boom"}}}
	code, out, errs := dvRun(t, f, true, "enable")
	if code != 1 || !strings.Contains(out, "boom") || !strings.Contains(errs, "failed") {
		t.Fatalf("code %d: %s %s", code, out, errs)
	}
}

func TestDVEnableRefusals(t *testing.T) {
	f := &fakeDVClient{status: helper.DVStatus{Supported: false, Reason: "ARMv6"}}
	if code, _, errs := dvRun(t, f, true, "enable"); code != 1 || !strings.Contains(errs, "ARMv6") || f.enables != 0 {
		t.Errorf("unsupported: %d %s", code, errs)
	}
	f = &fakeDVClient{status: helper.DVStatus{Supported: true}}
	if code, _, errs := dvRun(t, f, false, "enable"); code != 1 || !strings.Contains(errs, "root") || f.enables != 0 {
		t.Errorf("not root: %d %s", code, errs)
	}
	f = &fakeDVClient{status: helper.DVStatus{Supported: true}, enableErr: errors.New("busy")}
	if code, _, errs := dvRun(t, f, true, "enable"); code != 1 || !strings.Contains(errs, "busy") {
		t.Errorf("enable error: %d %s", code, errs)
	}
}

func TestDVEnableJoinsRunningJob(t *testing.T) {
	f := &fakeDVClient{status: helper.DVStatus{Supported: true, Job: "running"}, jobs: []helper.DVJob{{Job: "done", Log: "ok"}}}
	code, out, _ := dvRun(t, f, true, "enable")
	if code != 0 || f.enables != 0 || !strings.Contains(out, "already running") {
		t.Fatalf("code %d enables %d: %s", code, f.enables, out)
	}
}

func TestDVEnableLostHelper(t *testing.T) {
	f := &fakeDVClient{status: helper.DVStatus{Supported: true}, jobErr: errors.New("gone")}
	if code, _, errs := dvRun(t, f, true, "enable"); code != 1 || !strings.Contains(errs, "lost the helper") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestDVDisableAndStatus(t *testing.T) {
	f := &fakeDVClient{status: helper.DVStatus{Supported: true, Enabled: true, Active: true, LegacyUnits: []string{"old.service"}}}
	if code, _, _ := dvRun(t, f, true, "disable"); code != 0 || f.disables != 1 {
		t.Errorf("disable: %d %d", code, f.disables)
	}
	code, out, _ := dvRun(t, f, false, "status")
	if code != 0 || !strings.Contains(out, "enabled:    yes") || !strings.Contains(out, "old.service") {
		t.Errorf("status: %d %s", code, out)
	}
	code, out, _ = dvRun(t, f, false, "status", "--json")
	if code != 0 || !strings.Contains(out, `"enabled": true`) {
		t.Errorf("status json: %d %s", code, out)
	}
}

func TestDVUsage(t *testing.T) {
	f := &fakeDVClient{}
	if code, _, errs := dvRun(t, f, true); code != 2 || !strings.Contains(errs, "Usage: aslcp dv") {
		t.Errorf("no args: %d", code)
	}
	if code, out, _ := dvRun(t, f, true, "--help"); code != 0 || !strings.Contains(out, "Usage: aslcp dv") {
		t.Errorf("help: %d", code)
	}
	if code, _, _ := dvRun(t, f, true, "frobnicate"); code != 2 {
		t.Errorf("unknown: %d", code)
	}
	if code, _, _ := dvRun(t, f, true, "enable", "extra"); code != 2 {
		t.Errorf("extra arg: %d", code)
	}
}

func TestNewLines(t *testing.T) {
	cases := []struct{ prev, cur, want []string }{
		{nil, []string{"a", "b"}, []string{"a", "b"}},
		{[]string{"a", "b"}, []string{"a", "b", "c"}, []string{"c"}},
		{[]string{"a", "b", "c"}, []string{"b", "c", "d"}, []string{"d"}},
		{[]string{"a"}, []string{"x", "y"}, []string{"x", "y"}},
		{[]string{"a", "b"}, []string{"a", "b"}, []string{}},
	}
	for i, c := range cases {
		got := newLines(c.prev, c.cur)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("case %d: %v want %v", i, got, c.want)
		}
	}
}
