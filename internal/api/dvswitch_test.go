package api_test

import (
	"context"
	"errors"
	"testing"

	"github.com/artw1/aslcp/internal/helper"
)

type fakeDV struct {
	installs, uninstalls int
	err                  error
}

func (f *fakeDV) DVSwitchStatus(context.Context) (helper.DVSwitchStatus, error) {
	return helper.DVSwitchStatus{Supported: true, CanInstall: true, Packages: []helper.DVSwitchPkg{}}, f.err
}
func (f *fakeDV) DVSwitchJob(context.Context) (helper.DVSwitchJobs, error) {
	return helper.DVSwitchJobs{Install: helper.DVSwitchJob{Job: "idle"}}, f.err
}
func (f *fakeDV) DVSwitchInstall(context.Context) (helper.DVSwitchStatus, error) {
	f.installs++
	return helper.DVSwitchStatus{Install: helper.DVSwitchJob{Job: "running"}}, f.err
}
func (f *fakeDV) DVSwitchUninstall(context.Context) (helper.DVSwitchStatus, error) {
	f.uninstalls++
	return helper.DVSwitchStatus{Uninstall: helper.DVSwitchJob{Job: "running"}}, f.err
}

var dvEndpoints = [][2]string{{"GET", "/api/dvswitch"}, {"GET", "/api/dvswitch/job"}, {"POST", "/api/dvswitch/install"}, {"POST", "/api/dvswitch/uninstall"}}

func TestDVSwitchRoles(t *testing.T) {
	f := &fakeDV{}
	e := newEnv(t, opts{dvswitch: f})
	for _, ep := range dvEndpoints {
		expectErr(t, e, "", ep[0], ep[1], nil, 401, "unauthorized")
		expectErr(t, e, "viewer", ep[0], ep[1], nil, 403, "forbidden")
		expectErr(t, e, "op", ep[0], ep[1], nil, 403, "forbidden")
	}
	if f.installs+f.uninstalls != 0 {
		t.Fatal("a non-admin reached the helper")
	}
	var st helper.DVSwitchStatus
	if c := e.do("admin", "GET", "/api/dvswitch", nil, &st); c != 200 || !st.CanInstall {
		t.Fatalf("status: %d %+v", c, st)
	}
	var jobs helper.DVSwitchJobs
	if c := e.do("admin", "GET", "/api/dvswitch/job", nil, &jobs); c != 200 || jobs.Install.Job != "idle" {
		t.Fatalf("job: %d %+v", c, jobs)
	}
	if c := e.do("admin", "POST", "/api/dvswitch/install", nil, &st); c != 202 || st.Install.Job != "running" || f.installs != 1 {
		t.Fatalf("install: %d %+v", c, st)
	}
	if c := e.do("admin", "POST", "/api/dvswitch/uninstall", nil, &st); c != 202 || st.Uninstall.Job != "running" || f.uninstalls != 1 {
		t.Fatalf("uninstall: %d %+v", c, st)
	}
}

func TestDVSwitchUnavailable(t *testing.T) {
	e := newEnv(t, opts{})
	for _, ep := range dvEndpoints {
		expectErr(t, e, "admin", ep[0], ep[1], nil, 503, "dvswitch_unavailable")
	}
}

func TestDVSwitchErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{helper.ErrBusy, 409, "job_running"},
		{helper.ErrAptBusy, 409, "apt_busy"},
		{helper.ErrDVAlreadyInstalled, 409, "already_installed"},
		{helper.ErrDVUnsupported, 409, "unsupported_platform"},
		{helper.ErrDVClockUnset, 409, "clock_unset"},
		{helper.ErrDVOutsideASLCP, 409, "not_aslcp_installed"},
		{helper.ErrDVKeyMismatch, 502, "key_mismatch"},
		{&helper.UnavailableError{Reason: "helper not running", Err: errors.New("x")}, 503, "dvswitch_unavailable"},
		{errors.New("boom"), 502, "helper_error"},
	}
	for _, c := range cases {
		e := newEnv(t, opts{dvswitch: &fakeDV{err: c.err}})
		expectErr(t, e, "admin", "POST", "/api/dvswitch/install", nil, c.status, c.code)
		expectErr(t, e, "admin", "POST", "/api/dvswitch/uninstall", nil, c.status, c.code)
	}
}
