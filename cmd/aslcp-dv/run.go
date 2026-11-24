package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/artw1/aslcp/internal/dv/bridge"
	"github.com/artw1/aslcp/internal/dv/config"
	"github.com/artw1/aslcp/internal/dv/ctl"
	_ "github.com/artw1/aslcp/internal/dv/network/bm"       // registers BM
	_ "github.com/artw1/aslcp/internal/dv/network/homebrew" // registers TGIF and DMR
	_ "github.com/artw1/aslcp/internal/dv/network/loopback" // registers LOOP
	_ "github.com/artw1/aslcp/internal/dv/network/m17"      // registers M17
	_ "github.com/artw1/aslcp/internal/dv/network/nxdn"     // registers NXDN
	_ "github.com/artw1/aslcp/internal/dv/network/p25"      // registers P25
	_ "github.com/artw1/aslcp/internal/dv/network/ysf"      // registers YSF
	"github.com/artw1/aslcp/internal/dv/usrp"
	"github.com/artw1/aslcp/internal/dv/vocoder"
)

// exitConfig is EX_CONFIG: systemd's RestartPreventExitStatus=78 stops the
// restart loop on a bad config file.
const exitConfig = 78

const bindRetry = 2 * time.Second

// pending answers ctl until the router exists (the USRP port may still be
// held by another process).
type pending struct {
	h       atomic.Pointer[bridge.Router]
	started time.Time
	cfg     ctl.Config
	err     atomic.Value // string
}

func (p *pending) router() *bridge.Router { return p.h.Load() }

func (p *pending) st() ctl.Status {
	s := ctl.Status{Version: version, Started: p.started, Updated: p.started, Networks: []ctl.NetStatus{}}
	s.USRP.Listen, s.USRP.Peer = p.cfg.USRP.Listen, p.cfg.USRP.Peer
	s.Vocoder.Addr = p.cfg.Vocoder.MD380
	return s
}

func (p *pending) notReady() error {
	msg := "starting"
	if e, _ := p.err.Load().(string); e != "" {
		msg = "usrp listen: " + e
	}
	return &ctl.Error{Code: ctl.CodeInternal, Msg: msg}
}

func (p *pending) Status(ctx context.Context) (ctl.Status, error) {
	if r := p.router(); r != nil {
		return r.Status(ctx)
	}
	s := p.st()
	if e, _ := p.err.Load().(string); e != "" {
		s.Networks = []ctl.NetStatus{{Mode: "usrp", Login: ctl.LoginFailed, Detail: "listen: " + e}}
	}
	return s, nil
}

func (p *pending) Mode(ctx context.Context, a ctl.ModeArgs) (ctl.Status, error) {
	if r := p.router(); r != nil {
		return r.Mode(ctx, a)
	}
	return ctl.Status{}, p.notReady()
}

func (p *pending) Tune(ctx context.Context, a ctl.TuneArgs) (ctl.Status, error) {
	if r := p.router(); r != nil {
		return r.Tune(ctx, a)
	}
	return ctl.Status{}, p.notReady()
}

func (p *pending) Unlink(ctx context.Context, a ctl.UnlinkArgs) (ctl.Status, error) {
	if r := p.router(); r != nil {
		return r.Unlink(ctx, a)
	}
	return ctl.Status{}, p.notReady()
}

func (p *pending) Reload(ctx context.Context) (ctl.Status, error) {
	if r := p.router(); r != nil {
		return r.Reload(ctx)
	}
	return ctl.Status{}, p.notReady()
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", ctl.ConfigPath, "config file")
	sock := fs.String("ctl", "", "control socket (default: config ctl.socket, else "+ctl.SocketPath+")")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	lg := log.New(stderr, "aslcp-dv: ", 0)

	cfg, found, err := config.Load(*cfgPath)
	if err != nil {
		lg.Printf("config %s: %v", *cfgPath, err)
		return exitConfig
	}
	if !found {
		lg.Printf("no config at %s, using defaults", *cfgPath)
	}
	path := *sock
	if path == "" {
		path = cfg.Ctl.Socket
	}
	if path == "" {
		path = ctl.SocketPath
	}
	lg.Printf("version %s, usrp %s -> %s, md380 %s", version, cfg.USRP.Listen, cfg.USRP.Peer, cfg.Vocoder.MD380)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ln, err := ctl.Listen(path)
	if err != nil {
		lg.Printf("control socket: %v", err)
		return 1
	}
	defer os.Remove(path)

	pend := &pending{started: time.Now(), cfg: cfg}
	srv := ctl.NewServer(pend, ctl.ServerOptions{AllowUIDs: cfg.Ctl.AllowUIDs, Log: lg})
	srvDone := make(chan struct{})
	go func() {
		if err := srv.Serve(ctx, ln); err != nil {
			lg.Printf("control socket: %v", err)
			cancel()
		}
		close(srvDone)
	}()

	md := vocoder.NewMD380(cfg.Vocoder.MD380, time.Duration(cfg.Vocoder.TimeoutMS)*time.Millisecond)
	defer md.Close()

	ep := bindUSRP(ctx, cfg, pend, lg)
	if ep == nil { // cancelled while waiting
		<-srvDone
		return 0
	}
	defer ep.Close()

	var router *bridge.Router
	router = bridge.New(bridge.Options{
		USRP:         ep,
		AMBE:         md,
		Config:       cfg,
		Version:      version,
		VocoderStats: md.Stats,
		Emit:         srv.Publish,
		Log:          lg,
		Reload: func() (ctl.Config, error) {
			c, _, err := config.Load(*cfgPath)
			if err != nil {
				lg.Printf("reload: %v (keeping the old config)", err)
				return ctl.Config{}, err
			}
			md.Reconfigure(c.Vocoder.MD380, time.Duration(c.Vocoder.TimeoutMS)*time.Millisecond)
			return c, nil
		},
	})
	pend.h.Store(router)

	// SIGHUP is reload.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				rctx, c := context.WithTimeout(ctx, 5*time.Second)
				if _, err := router.Reload(rctx); err != nil {
					lg.Printf("reload: %v", err)
				} else {
					lg.Printf("config reloaded")
				}
				c()
			}
		}
	}()
	if cfg.Resume != nil {
		go func() {
			rctx, c := context.WithTimeout(ctx, 5*time.Second)
			defer c()
			// The router needs a moment to start its loop.
			if _, err := router.Tune(rctx, *cfg.Resume); err != nil {
				lg.Printf("resume: %v", err)
			}
		}()
	}

	lg.Printf("running, control socket %s", path)
	router.Run(ctx)
	cancel()
	<-srvDone
	lg.Printf("stopped")
	return 0
}

// bindUSRP binds the USRP socket, retrying every bindRetry (another process
// may hold the port during an upgrade). It returns nil if ctx ends first.
func bindUSRP(ctx context.Context, cfg ctl.Config, p *pending, lg *log.Logger) *usrp.Endpoint {
	var last string
	for {
		ep, err := usrp.Listen(cfg.USRP.Listen, cfg.USRP.Peer)
		if err == nil {
			p.err.Store("")
			return ep
		}
		if s := err.Error(); s != last {
			lg.Printf("usrp: %v (retrying every %v)", err, bindRetry)
			last = s
		}
		p.err.Store(last)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(bindRetry):
		}
	}
}
