// Command aslcp-node is the native node: one radio (the SHARI) and exactly
// one active network mode, AllStar, EchoLink or one of the digital voice
// networks. Whatever mode is active, its audio goes out to the radio and the
// radio's audio goes out to it; networks are never bridged.
//
// It reads /etc/aslcp/node.json (a missing file is a first boot: the node
// runs idle), serves the control API on /run/aslcp-node/control.sock and
// logs to stderr. SIGHUP reloads the config; SIGINT and SIGTERM stop it.
// Every exit path unkeys the transmitter.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/artw1/aslcp/internal/native/audio"
	_ "github.com/artw1/aslcp/internal/native/mode/all"
	"github.com/artw1/aslcp/internal/native/node"
	"github.com/artw1/aslcp/internal/native/radio"
)

var version = "dev"

// exitConfig (EX_CONFIG) tells systemd that restarting cannot help.
const exitConfig = 78

// exitRestart (EX_TEMPFAIL) asks systemd (Restart=on-failure) to start the
// daemon again, after the radio wiring changed.
const exitRestart = 75

// webUser is the web app's user, which may use the control socket.
const webUser = "aslcp"

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func run(args []string, stderr io.Writer) (code int) {
	fs := flag.NewFlagSet("aslcp-node", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", node.DefaultConfigPath, "config file")
	sock := fs.String("socket", node.DefaultSocket, "control socket")
	debug := fs.Bool("debug", false, "log debug messages")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stderr, "aslcp-node", version)
		return 0
	}
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := node.LoadConfig(*cfgPath)
	if err != nil {
		log.Error("config", "err", err)
		return exitConfig
	}

	rc := cfg.Station.Radio.Wiring()
	r, err := radio.Open(rc)
	absent := false
	switch {
	case errors.Is(err, radio.ErrNoRadio):
		log.Warn("no radio interface found; running without one", "err", err)
		r, absent = radio.NewFake(), true
	case err != nil:
		log.Error("radio", "err", err)
		return 1
	}
	// Every exit path, a panic included, unkeys the transmitter.
	defer func() {
		if p := recover(); p != nil {
			log.Error("panic", "err", p)
			code = 2
		}
		_ = r.SetPTT(false)
		_ = r.Close()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// The radio is opened once. When a saved station changes its wiring
	// (PTT GPIO, polarity, SA818 port) the daemon exits with exitRestart
	// and systemd starts it again on the new wiring. The short wait lets
	// the save's reply reach the app first.
	var restart atomic.Bool
	radioChanged := func() {
		log.Info("radio wiring changed; restarting to reopen the radio")
		time.Sleep(time.Second)
		restart.Store(true)
		stop()
	}

	e, err := node.New(cfg, node.Options{
		Radio: r, RadioAbsent: absent, RadioConfig: rc,
		ConfigPath: *cfgPath, Log: log, RadioChanged: radioChanged,
	})
	if err != nil {
		log.Error("node", "err", err)
		return 1
	}
	defer e.Close()

	if err := e.ProgramIfChanged(); err != nil {
		log.Warn("radio channel", "err", err)
	}
	if err := e.Start(ctx); err != nil {
		log.Warn("restoring the active mode", "err", err)
	}

	ln, err := node.Listen(*sock)
	if err != nil {
		log.Error("control", "err", err)
		return 1
	}
	srv := node.NewServer(e, node.ServerOptions{Allow: allowed(), Log: log})
	go func() {
		if err := srv.Serve(ctx, ln); err != nil {
			log.Error("control", "err", err)
			stop()
		}
	}()
	go e.WatchConfig(ctx, 5*time.Second)
	go reloadOnHUP(ctx, e, log)

	log.Info("running", "version", version, "radio", !absent, "config", *cfgPath, "socket", *sock)
	clock := audio.NewRealClock()
	defer clock.Stop()
	e.Run(ctx, clock)
	log.Info("stopping")
	if restart.Load() {
		return exitRestart
	}
	return 0
}

func reloadOnHUP(ctx context.Context, e *node.Engine, log *slog.Logger) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			if err := e.Reload(ctx); err != nil {
				log.Warn("config reload", "err", err)
			} else {
				log.Info("config reloaded")
			}
		}
	}
}

// allowed lets root, this daemon's own user and the web app's user use the
// control socket.
func allowed() func(int) bool {
	me := os.Getuid()
	web := -1
	if u, err := user.Lookup(webUser); err == nil {
		if n, err := strconv.Atoi(u.Uid); err == nil {
			web = n
		}
	}
	return func(uid int) bool { return uid == 0 || uid == me || (web >= 0 && uid == web) }
}
