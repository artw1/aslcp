package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/helper"
)

type fakeDV struct {
	installs, uninstalls int
	err                  error
	last                 helper.DVSwitchRequest
	calls                int
	cfgErr               error
	listMode             string
	listCalls            int
	listErr              error
	dmrCalls             int
}

func (f *fakeDV) rec(r helper.DVSwitchRequest) (helper.DVApplyResult, error) {
	f.last = r
	f.calls++
	if f.cfgErr != nil {
		return helper.DVApplyResult{}, f.cfgErr
	}
	return helper.DVApplyResult{Files: []string{"mmdvm_bridge"}, RestartHint: []string{"mmdvm_bridge"}}, nil
}
func (f *fakeDV) DVSwitchSetup(context.Context) (helper.DVSwitchSetup, error) {
	return helper.DVSwitchSetup{Identity: helper.DVIdentityView{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01"},
		DMR: helper.DVDMRView{Address: "192.0.2.10", Port: 62031, PasswordSet: true}, Files: []helper.DVFileView{}}, f.err
}
func (f *fakeDV) DVSwitchInfo(context.Context) (helper.DVSwitchInfo, error) {
	return helper.DVSwitchInfo{Found: true, Mode: "DMR"}, nil
}
func (f *fakeDV) DVSwitchDMRStatus(context.Context) (helper.DVDMRStatus, error) {
	f.dmrCalls++
	return helper.DVDMRStatus{State: "logged_in", Master: "3103.master.brandmeister.network:62031"}, f.err
}
func (f *fakeDV) DVSwitchIdentity(_ context.Context, r helper.DVSwitchRequest) (helper.DVApplyResult, error) {
	return f.rec(r)
}
func (f *fakeDV) DVSwitchCredentials(_ context.Context, r helper.DVSwitchRequest) (helper.DVApplyResult, error) {
	return f.rec(r)
}
func (f *fakeDV) DVSwitchModes(_ context.Context, r helper.DVSwitchRequest) (helper.DVApplyResult, error) {
	return f.rec(r)
}
func (f *fakeDV) DVSwitchStartup(_ context.Context, r helper.DVSwitchRequest) (helper.DVApplyResult, error) {
	return f.rec(r)
}
func (f *fakeDV) DVSwitchRollback(_ context.Context, r helper.DVSwitchRequest) (helper.DVApplyResult, error) {
	return f.rec(r)
}
func (f *fakeDV) DVSwitchRestart(_ context.Context, r helper.DVSwitchRequest) (helper.DVRestartResult, error) {
	f.last = r
	f.calls++
	if f.cfgErr != nil {
		return helper.DVRestartResult{}, f.cfgErr
	}
	return helper.DVRestartResult{Action: "restart", Units: []string{"analog_bridge"}, Skipped: []string{}, States: []helper.DVSwitchUnit{}}, nil
}

func (f *fakeDV) DVSwitchLists(_ context.Context, mode string) (helper.DVList, error) {
	f.listMode = mode
	f.listCalls++
	if f.listErr != nil {
		return helper.DVList{}, f.listErr
	}
	return helper.DVList{Mode: mode, ETag: "abc123", Items: []helper.DVListItem{
		{Value: "192.0.2.10", Name: "Example Master", Detail: "ID 1", Address: "192.0.2.10", Port: 62031}}}, nil
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

var dvCfgEndpoints = [][3]string{
	{"GET", "/api/dvswitch/setup", ""},
	{"PUT", "/api/dvswitch/identity", `{"callsign":"N0CALL","dmrId":"1234567","essid":"01"}`},
	{"PUT", "/api/dvswitch/credentials", `{"dmrPassword":"example-pass"}`},
	{"PUT", "/api/dvswitch/modes", `{"dmr":true}`},
	{"PUT", "/api/dvswitch/startup", `{"useCurrent":true}`},
	{"POST", "/api/dvswitch/restart", `{"scope":"all"}`},
	{"POST", "/api/dvswitch/rollback", `{"file":"analog_bridge"}`},
}

func dvBody(s string) any {
	if s == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic(err)
	}
	return m
}

func TestDVSwitchCfgRoles(t *testing.T) {
	f := &fakeDV{}
	e := newEnv(t, opts{dvswitch: f})
	for _, ep := range dvCfgEndpoints {
		expectErr(t, e, "", ep[0], ep[1], dvBody(ep[2]), 401, "unauthorized")
		expectErr(t, e, "viewer", ep[0], ep[1], dvBody(ep[2]), 403, "forbidden")
		expectErr(t, e, "op", ep[0], ep[1], dvBody(ep[2]), 403, "forbidden")
	}
	if f.calls != 0 {
		t.Fatal("a non-admin reached the helper")
	}
	for _, ep := range dvCfgEndpoints {
		if c := e.do("admin", ep[0], ep[1], dvBody(ep[2]), nil); c != 200 {
			t.Errorf("%s %s: %d", ep[0], ep[1], c)
		}
	}
	e2 := newEnv(t, opts{})
	for _, ep := range dvCfgEndpoints {
		expectErr(t, e2, "admin", ep[0], ep[1], dvBody(ep[2]), 503, "dvswitch_unavailable")
	}
}

func TestDVSwitchPasswordNeverEchoed(t *testing.T) {
	const pw = "example-pass"
	f := &fakeDV{}
	e := newEnv(t, opts{dvswitch: f})
	code, raw, _ := e.raw("PUT", "/api/dvswitch/credentials", map[string]any{"dmrAddress": "192.0.2.10", "dmrPort": 62031, "dmrPassword": pw}, e.ck["admin"], nil)
	if code != 200 || strings.Contains(string(raw), pw) {
		t.Fatalf("%d %s", code, raw)
	}
	if !strings.Contains(string(raw), `"passwordSet":true`) {
		t.Errorf("no passwordSet: %s", raw)
	}
	if f.last.Credentials == nil || f.last.Credentials.Password == nil || *f.last.Credentials.Password != pw {
		t.Error("password not passed to the helper")
	}
	// refused write: no echo either
	f.cfgErr = helper.ErrDVInvalid
	code, raw, _ = e.raw("PUT", "/api/dvswitch/credentials", map[string]any{"dmrPassword": pw}, e.ck["admin"], nil)
	if code != 400 || strings.Contains(string(raw), pw) || !strings.Contains(string(raw), "invalid_field") {
		t.Fatalf("%d %s", code, raw)
	}
	aud, err := e.st.ListAudit(50)
	if err != nil || len(aud) == 0 {
		t.Fatalf("%v %d", err, len(aud))
	}
	for _, a := range aud {
		if strings.Contains(a.Detail, pw) || strings.Contains(a.Action, pw) {
			t.Errorf("audit leaks the password: %+v", a)
		}
	}
	found := false
	for _, a := range aud {
		found = found || a.Action == "dvswitch.credentials" && strings.Contains(a.Detail, "dmrPassword")
	}
	if !found {
		t.Error("audit lacks the key name")
	}
}

func TestDVSwitchIdentityNXDNID(t *testing.T) {
	f := &fakeDV{}
	e := newEnv(t, opts{dvswitch: f})
	body := map[string]any{"callsign": "N0CALL", "dmrId": "1234567", "essid": "01", "nxdnId": "12346"}
	if code, raw, _ := e.raw("PUT", "/api/dvswitch/identity", body, e.ck["admin"], nil); code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	if f.last.Identity == nil || f.last.Identity.NXDNID != "12346" {
		t.Fatalf("%+v", f.last.Identity)
	}
	delete(body, "nxdnId")
	e.raw("PUT", "/api/dvswitch/identity", body, e.ck["admin"], nil)
	if f.last.Identity == nil || f.last.Identity.NXDNID != "" {
		t.Fatalf("%+v", f.last.Identity)
	}
	aud, _ := e.st.ListAudit(50)
	found := false
	for _, a := range aud {
		found = found || a.Action == "dvswitch.identity" && strings.Contains(a.Detail, "nxdnId") && !strings.Contains(a.Detail, "12346")
	}
	if !found {
		t.Error("audit lacks the key name")
	}
}

func TestDVSwitchCfgErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{helper.ErrDVInvalid, 400, "invalid_field"},
		{helper.ErrDVNotInstalled, 409, "not_installed"},
		{helper.ErrDVNoHistory, 404, "no_history"},
		{helper.ErrDVNoInfo, 409, "no_info"},
		{&helper.ConflictError{Files: []string{"analog_bridge"}}, 409, "conflict"},
		{helper.ErrBusy, 409, "job_running"},
	}
	for _, c := range cases {
		e := newEnv(t, opts{dvswitch: &fakeDV{cfgErr: c.err}})
		expectErr(t, e, "admin", "PUT", "/api/dvswitch/modes", map[string]any{"dmr": true}, c.status, c.code)
		expectErr(t, e, "admin", "POST", "/api/dvswitch/rollback", map[string]any{"file": "analog_bridge"}, c.status, c.code)
	}
}

func TestDVSwitchRestartWarning(t *testing.T) {
	e := newEnv(t, opts{dvswitch: &fakeDV{}})
	var r struct {
		Action, Warning string
		Units           []string
	}
	if c := e.do("admin", "POST", "/api/dvswitch/restart", map[string]any{"scope": "bridge"}, &r); c != 200 || r.Warning == "" || len(r.Units) != 1 {
		t.Fatalf("%d %+v", c, r)
	}
}

func TestDVSwitchListsRolesAndMode(t *testing.T) {
	f := &fakeDV{}
	e := newEnv(t, opts{dvswitch: f})
	expectErr(t, e, "", "GET", "/api/dvswitch/lists?mode=dmr", nil, 401, "unauthorized")
	// viewers cannot pick, operators and admins can
	expectErr(t, e, "viewer", "GET", "/api/dvswitch/lists?mode=dmr", nil, 403, "forbidden")
	if f.listCalls != 0 {
		t.Fatal("a viewer reached the helper")
	}
	for _, role := range []string{"op", "admin"} {
		var l helper.DVList
		if c := e.do(role, "GET", "/api/dvswitch/lists?mode=ysf", nil, &l); c != 200 || l.Mode != "ysf" || len(l.Items) != 1 || l.Items[0].Port != 62031 {
			t.Fatalf("%s: %d %+v", role, c, l)
		}
	}
	for _, m := range []string{"", "DMR", "dstar", "../x", "dmr,ysf", "dmr/../ysf"} {
		before := f.listCalls
		expectErr(t, e, "admin", "GET", "/api/dvswitch/lists?mode="+m, nil, 400, "bad_request")
		if f.listCalls != before {
			t.Errorf("mode %q reached the helper", m)
		}
	}
	expectErr(t, e, "admin", "GET", "/api/dvswitch/lists", nil, 400, "bad_request")
}

func TestDVSwitchListsNoList(t *testing.T) {
	e := newEnv(t, opts{dvswitch: &fakeDV{listErr: helper.ErrDVNoList}})
	expectErr(t, e, "admin", "GET", "/api/dvswitch/lists?mode=p25", nil, 404, "no_list")
	e = newEnv(t, opts{})
	expectErr(t, e, "admin", "GET", "/api/dvswitch/lists?mode=p25", nil, 503, "dvswitch_unavailable")
	e = newEnv(t, opts{dvswitch: &fakeDV{listErr: errors.New("boom")}})
	expectErr(t, e, "admin", "GET", "/api/dvswitch/lists?mode=p25", nil, 502, "helper_error")
}

func TestDVSwitchListsETag(t *testing.T) {
	e := newEnv(t, opts{dvswitch: &fakeDV{}})
	code, _, h := e.raw("GET", "/api/dvswitch/lists?mode=nxdn", nil, e.ck["admin"], nil)
	tag := h.Get("ETag")
	if code != 200 || tag != `"nxdn-abc123"` {
		t.Fatalf("%d %q", code, tag)
	}
	code, b, _ := e.raw("GET", "/api/dvswitch/lists?mode=nxdn", nil, e.ck["admin"], map[string]string{"If-None-Match": tag})
	if code != 304 || len(b) != 0 {
		t.Fatalf("%d %q", code, b)
	}
	// the tag of another mode does not match
	code, _, _ = e.raw("GET", "/api/dvswitch/lists?mode=p25", nil, e.ck["admin"], map[string]string{"If-None-Match": tag})
	if code != 200 {
		t.Fatal(code)
	}
}

func TestDVSwitchApplyWarningsInReply(t *testing.T) {
	f := &fakeDVWarn{fakeDV: &fakeDV{}}
	e := newEnv(t, opts{dvswitch: f})
	var r struct {
		ApplyWarnings []string `json:"applyWarnings"`
	}
	if c := e.do("admin", "PUT", "/api/dvswitch/startup", map[string]any{"useCurrent": true}, &r); c != 200 || len(r.ApplyWarnings) != 1 {
		t.Fatalf("%d %+v", c, r)
	}
}

type fakeDVWarn struct{ *fakeDV }

func (f *fakeDVWarn) DVSwitchStartup(_ context.Context, r helper.DVSwitchRequest) (helper.DVApplyResult, error) {
	res, err := f.rec(r)
	res.Warnings = []string{"YSF startup was not changed: the current reflector is not in YSFHosts.txt"}
	return res, err
}

func TestDVSwitchDMRStatusRoles(t *testing.T) {
	f := &fakeDV{}
	e := newEnv(t, opts{dvswitch: f})
	expectErr(t, e, "", "GET", "/api/dvswitch/dmr-status", nil, 401, "unauthorized")
	expectErr(t, e, "viewer", "GET", "/api/dvswitch/dmr-status", nil, 403, "forbidden")
	if f.dmrCalls != 0 {
		t.Fatal("a viewer reached the helper")
	}
	for _, role := range []string{"op", "admin"} {
		var r helper.DVDMRStatus
		if c := e.do(role, "GET", "/api/dvswitch/dmr-status", nil, &r); c != 200 || r.State != "logged_in" || !strings.HasPrefix(r.Master, "3103.") {
			t.Fatalf("%s: %d %+v", role, c, r)
		}
	}
	// read-only: no write method is routed
	expectErr(t, e, "admin", "POST", "/api/dvswitch/dmr-status", map[string]any{}, 404, "not_found")
	e = newEnv(t, opts{})
	expectErr(t, e, "op", "GET", "/api/dvswitch/dmr-status", nil, 503, "dvswitch_unavailable")
	e = newEnv(t, opts{dvswitch: &fakeDV{err: errors.New("boom")}})
	expectErr(t, e, "op", "GET", "/api/dvswitch/dmr-status", nil, 502, "helper_error")
}

func TestDVSwitchRestartScopeDMR(t *testing.T) {
	f := &fakeDV{}
	e := newEnv(t, opts{dvswitch: f})
	// the restart stays admin only, the new scope included
	expectErr(t, e, "op", "POST", "/api/dvswitch/restart", map[string]any{"scope": "dmr"}, 403, "forbidden")
	if f.calls != 0 {
		t.Fatal("an operator reached the helper")
	}
	var r struct{ Action string }
	if c := e.do("admin", "POST", "/api/dvswitch/restart", map[string]any{"scope": "dmr"}, &r); c != 200 || f.last.Scope != "dmr" {
		t.Fatalf("%d %+v scope %q", c, r, f.last.Scope)
	}
}
