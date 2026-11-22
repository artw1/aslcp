package main

import (
	"context"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/helper"
)

// demoDV is the in-memory digital modes of demo mode: a stand-in for the
// root helper's dv ops (api.DVEngine) and for the engine's control socket
// (ctl.Handler). LOOP and BM are available.
type demoDV struct {
	mu      sync.Mutex
	enabled bool
	rec     *helper.DVPrivateRecord
	status  ctl.Status
	srv     *ctl.Server
	ident   helper.DVIdentityView
	nets    map[string]helper.DVNetworkView
}

func newDemoDV() *demoDV {
	now := time.Now()
	d := &demoDV{enabled: true}
	d.status = ctl.Status{
		Version: "demo", Started: now, Updated: now, Mode: ctl.ModeLoop,
		USRP:    ctl.USRPStatus{Listen: "127.0.0.1:34001", Peer: "127.0.0.1:32001"},
		Vocoder: ctl.VocoderStatus{OK: true, Addr: "127.0.0.1:2470"},
	}
	for _, m := range []string{ctl.ModeLoop, ctl.ModeBM, ctl.ModeTGIF, ctl.ModeDMR, ctl.ModeYSF, ctl.ModeP25, ctl.ModeNXDN} {
		d.status.Networks = append(d.status.Networks, ctl.NetStatus{
			Mode: m, Available: dvpanel.NativeMode(m), Enabled: m == ctl.ModeLoop, Login: ctl.LoginOff})
	}
	return d
}

// serve starts the control socket in a temporary directory and returns the
// path and a cleanup function.
func (d *demoDV) serve(ctx context.Context) (string, func(), error) {
	dir, err := os.MkdirTemp("", "aslcp-dv")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "ctl.sock")
	ln, err := listenCtl(path)
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	d.srv = ctl.NewServer(d, ctl.ServerOptions{AllowUIDs: []int{os.Geteuid()}, Log: log.New(os.Stderr, "demo-dv: ", 0)})
	go d.srv.Serve(ctx, ln)
	return path, func() { ln.Close(); os.RemoveAll(dir) }, nil
}

func listenCtl(path string) (net.Listener, error) { return ctl.Listen(path) }

// ---- ctl.Handler

func (d *demoDV) snap() ctl.Status { return d.status }

func (d *demoDV) Status(context.Context) (ctl.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snap(), nil
}
func (d *demoDV) Reload(ctx context.Context) (ctl.Status, error) { return d.Status(ctx) }

func (d *demoDV) net(mode string) *ctl.NetStatus {
	for i := range d.status.Networks {
		if d.status.Networks[i].Mode == mode {
			return &d.status.Networks[i]
		}
	}
	return nil
}

func (d *demoDV) Mode(_ context.Context, a ctl.ModeArgs) (ctl.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.net(a.Mode)
	if n == nil || !n.Available {
		return ctl.Status{}, &ctl.Error{Code: ctl.CodeNotAvailable, Msg: a.Mode + " is not available in this build"}
	}
	d.status.Mode, d.status.Target, d.status.Updated = a.Mode, nil, time.Now()
	return d.pub(), nil
}

func (d *demoDV) Tune(_ context.Context, a ctl.TuneArgs) (ctl.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.net(a.Mode)
	if n == nil || !n.Available {
		return ctl.Status{}, &ctl.Error{Code: ctl.CodeNotAvailable, Msg: a.Mode + " is not available in this build"}
	}
	t := a.Target
	d.status.Mode, d.status.Target, d.status.Updated = a.Mode, &t, time.Now()
	return d.pub(), nil
}

func (d *demoDV) Unlink(context.Context, ctl.UnlinkArgs) (ctl.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status.Target, d.status.Updated = nil, time.Now()
	return d.pub(), nil
}

// pub returns the status and tells the subscribers (d.mu held).
func (d *demoDV) pub() ctl.Status {
	st := d.snap()
	if d.srv != nil {
		d.srv.Publish(ctl.Event{Type: ctl.EvStatus, At: time.Now(), Status: &st})
	}
	return st
}

// ---- api.DVEngine

func (d *demoDV) DVStatus(context.Context) (helper.DVStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return helper.DVStatus{Supported: true, MD380Installed: true, Enabled: d.enabled, Active: d.enabled, MD380Active: d.enabled}, nil
}
func (d *demoDV) DVJob(context.Context) (helper.DVJob, error) { return helper.DVJob{}, nil }
func (d *demoDV) DVEnable(ctx context.Context) (helper.DVStatus, error) {
	d.mu.Lock()
	d.enabled = true
	d.mu.Unlock()
	return d.DVStatus(ctx)
}
func (d *demoDV) DVDisable(ctx context.Context) (helper.DVStatus, error) {
	d.mu.Lock()
	d.enabled = false
	d.mu.Unlock()
	return d.DVStatus(ctx)
}
func (d *demoDV) DVConfig(context.Context) (helper.DVConfigResult, error) {
	return helper.DVConfigResult{Path: ctl.ConfigPath}, nil
}
func (d *demoDV) DVRestart(ctx context.Context) (helper.DVStatus, error) { return d.DVStatus(ctx) }
func (d *demoDV) DVRestoreDiscard(ctx context.Context) (helper.DVStatus, error) {
	return d.DVStatus(ctx)
}
func (d *demoDV) DVPrivateGet(context.Context) (helper.DVPrivateReply, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return helper.DVPrivateReply{Record: d.rec}, nil
}
func (d *demoDV) DVPrivateSet(_ context.Context, r helper.DVPrivateRecord) (helper.DVPrivateReply, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rec = &r
	return helper.DVPrivateReply{Record: d.rec}, nil
}
func (d *demoDV) DVPrivateClear(context.Context) (helper.DVPrivateReply, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rec = nil
	return helper.DVPrivateReply{}, nil
}

// DVNetworkGet and DVNetworkSet keep the network settings in memory. The
// password is only remembered as set or not.
func (d *demoDV) DVNetworkGet(context.Context) (helper.DVNetworkReply, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.netReply(false), nil
}

func (d *demoDV) netReply(changed bool) helper.DVNetworkReply {
	r := helper.DVNetworkReply{Identity: d.ident, Rendered: true, Changed: changed}
	for _, m := range []string{ctl.ModeBM, ctl.ModeTGIF, ctl.ModeDMR, ctl.ModeNXDN} {
		if v, ok := d.nets[m]; ok {
			r.Networks = append(r.Networks, v)
		}
	}
	return r
}

func (d *demoDV) DVNetworkSet(_ context.Context, q helper.DVNetworkRequest) (helper.DVNetworkReply, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if q.Identity != nil {
		d.ident = *q.Identity
	}
	if q.Mode != "" {
		if d.nets == nil {
			d.nets = map[string]helper.DVNetworkView{}
		}
		v := d.nets[q.Mode]
		v.Mode = q.Mode
		if q.Enabled != nil {
			v.Enabled = *q.Enabled
		}
		if q.Address != nil {
			v.Address = *q.Address
		}
		if q.Port != nil {
			v.Port = *q.Port
		}
		if q.Password != nil && *q.Password != "" {
			v.PasswordSet = true
		}
		if q.ClearPassword {
			v.PasswordSet = false
		}
		if q.Options != nil {
			v.Options = q.Options
		}
		d.nets[q.Mode] = v
		if n := d.net(q.Mode); n != nil && n.Available && q.Enabled != nil {
			n.Enabled = *q.Enabled
		}
	}
	return d.netReply(true), nil
}
