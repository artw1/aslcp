// Command aslcp is the ASLCP server: a web control panel for AllStarLink 3.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/artw1/aslcp/internal/allscan"
	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/api"
	"github.com/artw1/aslcp/internal/auth"
	"github.com/artw1/aslcp/internal/backupsvc"
	"github.com/artw1/aslcp/internal/config"
	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/demo"
	"github.com/artw1/aslcp/internal/diag"
	"github.com/artw1/aslcp/internal/favstats"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/nodelist"
	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/schedules"
	"github.com/artw1/aslcp/internal/store"
	"github.com/artw1/aslcp/internal/susb"
	"github.com/artw1/aslcp/internal/system"
	"github.com/artw1/aslcp/internal/updates"
	"github.com/artw1/aslcp/internal/webui"
)

var version = "dev"

const devOrigin = "http://localhost:5173"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		os.Exit(adminMain(os.Args[2:]))
	}
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n  aslcp [options]            run the web server\n  aslcp admin <command>      manage accounts (create, reset-password, list); see: aslcp admin --help\n\nOptions:\n")
		flag.PrintDefaults()
	}
	cfgPath := flag.String("config", config.DefaultConfigPath, "path to aslcp.conf")
	dev := flag.Bool("dev", false, "development mode: print setup code, allow Vite origin, no Secure cookie")
	demoMode := flag.Bool("demo", false, "run against a simulated node (no Asterisk needed)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("aslcp", version)
		return
	}
	if err := run(*cfgPath, *dev, *demoMode); err != nil {
		if errors.Is(err, errRestart) {
			// A restore finished: systemd restarts the service on this
			// status (SuccessExitStatus/RestartForceExitStatus in the unit).
			os.Exit(restartExitCode)
		}
		log.Fatalf("aslcp: %v", err)
	}
}

// restartExitCode is the exit status that asks systemd for a restart after a
// restore (see packaging/systemd/aslcp.service).
const restartExitCode = 75

var errRestart = errors.New("restart requested")

func run(cfgPath string, dev, demoMode bool) error {
	// Keep the last log lines in memory for the diagnostics download.
	ring := diag.NewRing(500)
	logOut := io.MultiWriter(os.Stderr, ring)
	log.SetOutput(logOut)
	logger := log.New(logOut, "", log.LstdFlags)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	defaults := config.Defaults()
	if demoMode {
		if cfg.DataDir == defaults.DataDir {
			d, err := os.MkdirTemp("", "aslcp-demo-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(d)
			cfg.DataDir = d
		}
		if cfg.Listen == defaults.Listen {
			cfg.Listen = "127.0.0.1:8080"
		}
		cfg.NodelistURL = ""
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("datadir: %w", err)
	}

	// A restore stages its database; the swap happens before it is opened.
	if swapped, err := backupsvc.ApplyStaged(cfg.DataDir); err != nil {
		logger.Printf("restore: %v", err)
	} else if swapped {
		logger.Printf("restore: the restored database is in place (the previous one is aslcp.db.pre-restore)")
	}

	st, err := store.Open(filepath.Join(cfg.DataDir, "aslcp.db"))
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer st.Close()

	as, err := auth.NewService(st, cfg.DataDir)
	if err != nil {
		return err
	}
	as.DevMode = dev

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var wg sync.WaitGroup
	bg := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}

	nl := nodelist.New(nodelist.Options{URL: cfg.NodelistURL, DataDir: cfg.DataDir, Interval: cfg.NodelistRefresh, Logger: logger})
	proxy := &lateAMI{}
	setTuneSave := func(func(string)) {}

	if demoMode {
		sim, err := demo.Start(ctx)
		if err != nil {
			return err
		}
		defer sim.Wait()
		setTuneSave = func(f func(string)) { sim.OnTuneSave = f }
		nl.Set(demo.NodeList(), "demo", time.Now())
		client := ami.NewClient(ami.Config{Addr: sim.Addr, Username: demo.Username, Secret: demo.Secret, Events: "call",
			ReconnectMin: 500 * time.Millisecond, ReconnectMax: 5 * time.Second, PingInterval: 30 * time.Second})
		proxy.set(client)
		defer client.Close()
		if n, _ := st.CountUsers(); n == 0 {
			h, err := auth.HashPassword("demo")
			if err != nil {
				return err
			}
			if _, err := st.CreateUser("demo", h, store.RoleAdmin); err != nil {
				return err
			}
			fmt.Println("DEMO MODE: log in as user 'demo' with password 'demo' (admin)")
		}
		if favs, _ := st.ListFavorites(); len(favs) == 0 {
			// A few favorites so the activity scan has something to show: regular nodes,
			// one of ours, a private number and an EchoLink node (the last three are not scanned).
			for _, n := range demo.NodeList() {
				switch n.Node {
				case "50241", "2000", "27339", "43763", "555491", "28507", "29307", "27131", "27542", "2007", "1999", "3001234":
					_, _ = st.AddFavorite(n.Node, n.Callsign, "")
				}
			}
		}
		seedDemoSchedules(st, time.Now())
	} else {
		bg(func() { nl.Run(ctx) })
	}

	if code, err := as.EnsureSetupCode(); err != nil {
		return fmt.Errorf("setup code: %w", err)
	} else if code != "" {
		if dev {
			fmt.Printf("SETUP CODE: %s\n", code)
		}
		logger.Printf("first-run setup required: open %s (or run: aslcp admin create); code also in %s",
			setupLink(cfg.Listen, code, interfaceAddrs()), filepath.Join(cfg.DataDir, "setup-code"))
	}

	var upd *updates.Manager
	var power api.Power
	var cfgHelper configsvc.Helper
	var bkHelper backupsvc.Helper
	var dvsw api.DVSwitch
	var restart func()
	var restartReq atomic.Bool
	var firewall func(context.Context) (helper.Firewall, error)
	if demoMode {
		firewall = func(context.Context) (helper.Firewall, error) {
			return helper.Firewall{Present: true, Running: true, ServiceEnabled: true}, nil
		}
		dch := configsvc.NewDemoHelper()
		cfgHelper = dch
		setTuneSave(func(conf string) { dch.SetFile("simpleusb.conf", conf) })
		// Backup and restore run for real on temporary directories.
		demoDir, err := os.MkdirTemp("", "aslcp-demo-node-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(demoDir)
		demoFiles := map[string]string{}
		if list, err := dch.ConfigList(ctx); err == nil {
			for _, f := range list {
				if c, err := dch.ConfigRead(ctx, f.Path); err == nil {
					demoFiles[f.Path] = c.Content
				}
			}
		}
		ops, err := helper.NewDemoOps(demoDir, demoFiles)
		if err != nil {
			return err
		}
		bkHelper = helper.LocalBackup{Ops: ops}
		dh := updates.NewDemoHelper(version)
		upd = updates.New(dh, version, dh.DemoFetch())
		upd.FirstCheck = time.Second
		upd.OS = updates.NewOS(dh, st)
		upd.OS.FirstCheck, upd.OS.Tick = time.Second, time.Second
		power = dh
		dvsw = helper.NewDemoDVSwitch()
		bg(func() { upd.Run(ctx) })
		bg(func() { upd.OS.Run(ctx) })
	} else {
		hc := helper.NewClient(cfg.HelperSocket)
		cfgHelper = hc
		bkHelper = hc
		restart = func() { restartReq.Store(true); stop() }
		firewall = hc.FirewallStatus
		upd = updates.New(hc, version, updates.HTTPLatest(cfg.UpdatesURL, nil))
		upd.Logger = logger
		upd.OS = updates.NewOS(hc, st)
		upd.OS.Logger = logger
		power = hc
		dvsw = hc
		bg(func() { upd.Run(ctx) })
		bg(func() { upd.OS.Run(ctx) })
	}

	sys := system.New(version, proxy)
	var origins []string
	if dev {
		origins = []string{devOrigin}
	}
	cfgSvc := configsvc.New(cfgHelper, proxy, rpt.New(proxy))
	var confPath string
	if !demoMode {
		confPath = cfgPath
	}
	bk := backupsvc.New(backupsvc.Options{Helper: bkHelper, Store: st, DataDir: cfg.DataDir, ConfPath: confPath,
		Version: version, Logger: logger, Demo: demoMode})
	bg(func() { backupMaintenance(ctx, bk, proxy) })
	tune := susb.New(proxy)
	tune.Logger = logger
	if cs, ok := cfgHelper.(susb.ConfigSource); ok {
		tune.Config = cs
	}
	if rc, ok := cfgHelper.(susb.Recorder); ok {
		tune.Recorder = rc
	}
	rescan := make(chan struct{}, 1)
	fsOpts := favstats.Options{BaseURL: cfg.FavStatsURL, UserAgent: "aslcp/" + version, Logger: logger, LogRequests: cfg.FavStatsLog}
	if demoMode {
		// No real service to protect: a request every 2 s, each taking 0.8 s.
		fsOpts.Fetch = favstats.WithLatency(favstats.DemoFetcher(time.Now), 800*time.Millisecond)
		fsOpts.Interval = 2 * time.Second
	}
	favScan := favstats.New(fsOpts)
	favScan.SetRate(cfg.FavStatsRate)
	// Schedules: the API wires the engine's link controller and broadcast.
	sched := &schedules.Engine{Store: st, Logger: logger, StartDelay: 5 * time.Second}
	srv := api.New(api.Deps{
		FavStats:     favScan,
		Schedules:    sched,
		RescanNodes:  func() { kick(rescan) },
		Tuning:       tune,
		ConfigEditor: cfgSvc,
		Auth:         as, Store: st, AMI: proxy, Nodelist: nl, System: sys, Updates: upd, Power: power, DVSwitch: dvsw, Version: version,
		UI: webui.Handler(), AllowedOrigins: origins, AllScanDirs: allscan.DefaultDirs, Logger: logger,
		Log: ring, Config: cfg, ConfigPath: cfgPath, Firewall: firewall,
		PublicDefaultEnabled: demoMode,
		Backup:               bk, Restart: restart,
	})
	if demoMode {
		srv.AddSecret(demo.Secret)
	}
	bg(func() { srv.Run(ctx) })
	bg(func() { cleanupSessions(ctx, st) })

	if !demoMode {
		bg(func() { connectAMI(ctx, cfg, proxy, srv, logger, rescan) })
	} else {
		bg(func() { monitorNodes(ctx, proxy, srv, logger, rescan) })
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		stop()
		wg.Wait()
		return err
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          logger,
		// No WriteTimeout: SSE streams are long-lived (handlers set their own deadlines).
	}
	logger.Printf("aslcp %s listening on http://%s", version, ln.Addr())
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()

	select {
	case err := <-errc:
		stop()
		wg.Wait()
		return err
	case <-ctx.Done():
	}
	logger.Printf("shutting down")
	// Echo mode must not stay on: switch it off while the radio connection
	// is still up, then close that connection.
	tctx, tcancel := context.WithTimeout(context.Background(), 5*time.Second)
	tune.Close(tctx)
	tcancel()
	srv.Shutdown()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(sctx); err != nil {
		_ = httpSrv.Close()
	}
	wg.Wait()
	if restartReq.Load() {
		return errRestart
	}
	return nil
}

// backupMaintenance purges expired uploads and, after a restore, completes
// the health check of the restored node once Asterisk has had time to come
// back.
func backupMaintenance(ctx context.Context, bk *backupsvc.Service, proxy *lateAMI) {
	bk.RunHealth(ctx, func(ctx context.Context, nodes []string) []backupsvc.Check {
		checks := []backupsvc.Check{{Name: "ASLCP", OK: true, Detail: "restarted with the restored database"}}
		st := proxy.State()
		c := backupsvc.Check{Name: "Asterisk connection", OK: st.Connected, Detail: "connected"}
		if !st.Connected {
			c.Detail = "not connected yet"
			if st.Err != nil {
				c.Detail = st.Err.Error()
			}
		}
		checks = append(checks, c)
		if st.Connected {
			mctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			lines, err := proxy.Command(mctx, "module show like app_rpt")
			cancel()
			checks = append(checks, backupsvc.AppRptCheck(lines, err))
		} else {
			checks = append(checks, backupsvc.Check{Name: "app_rpt module", Detail: "waiting for the Asterisk connection"})
		}
		for _, n := range nodes {
			cc := backupsvc.Check{Name: "Node " + n, OK: true, Detail: "answers"}
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if lines, err := proxy.Command(cctx, "rpt show variables "+n); err != nil {
				cc.OK, cc.Detail = false, err.Error()
			} else if !backupsvc.NodeAnswers(lines) {
				cc.OK, cc.Detail = false, "app_rpt does not answer"
			}
			cancel()
			checks = append(checks, cc)
		}
		return checks
	})
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			bk.Purge()
		}
	}
}

func cleanupSessions(ctx context.Context, st *store.Store) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = st.DeleteExpiredSessions(time.Now())
		}
	}
}

// connectAMI resolves credentials, connects to Asterisk and starts the
// monitor once the local nodes are known. It retries forever; Asterisk may
// start after ASLCP.
func connectAMI(ctx context.Context, cfg config.Config, proxy *lateAMI, srv *api.Server, logger *log.Logger, rescan <-chan struct{}) {
	for {
		err := cfg.ResolveAMI()
		if err == nil {
			break
		}
		logger.Printf("ami: %v (retrying)", err)
		if !sleep(ctx, 15*time.Second) {
			return
		}
	}
	srv.AddSecret(cfg.AMISecret) // never to appear in diagnostics
	client := ami.NewClient(ami.Config{
		Addr: cfg.AMIAddr(), Username: cfg.AMIUsername, Secret: cfg.AMISecret, Events: "call",
		PingInterval: 30 * time.Second,
	})
	defer client.Close()
	proxy.set(client)
	monitorNodes(ctx, proxy, srv, logger, rescan)
}

func monitorNodes(ctx context.Context, proxy *lateAMI, srv *api.Server, logger *log.Logger, rescan <-chan struct{}) {
	client := proxy.get()
	rc := rpt.New(client)
	backoff := time.Second
	var nodes []string
	for {
		if err := client.WaitConnected(ctx); err != nil {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		var err error
		nodes, err = rc.LocalNodes(cctx)
		cancel()
		if err == nil && len(nodes) > 0 {
			break
		}
		if err == nil {
			err = errors.New("no local nodes configured")
		}
		logger.Printf("ami: listing local nodes: %v (retrying)", err)
		if !sleep(ctx, backoff) {
			return
		}
		if backoff *= 2; backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
	logger.Printf("monitoring local nodes %v", nodes)
	mon := rpt.NewMonitor(client, rpt.MonitorConfig{Nodes: nodes, Interval: 10 * time.Second})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); _ = mon.Run(ctx) }()
	go func() { defer wg.Done(); rescanNodes(ctx, client, rc, mon, rescan, logger) }()
	go func() { defer wg.Done(); srv.AttachMonitor(ctx, mon) }()
	wg.Wait()
}

// kick does a non-blocking send on a 1-buffered signal channel.
func kick(c chan<- struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

const (
	nodeRescanEvery = 5 * time.Minute // backstop for changes nothing else announced
	nodeRescanDelay = 2 * time.Second // let Asterisk settle after a reload
)

// rescanNodes keeps the monitored node set in step with rpt.conf: it
// re-reads the local nodes after each AMI reconnect (Asterisk restarted),
// shortly after a reload the panel triggered (rescan), and every 5 minutes.
// Errors and empty results keep the current set.
func rescanNodes(ctx context.Context, client *ami.Client, rc *rpt.Client, mon *rpt.Monitor, rescan <-chan struct{}, logger *log.Logger) {
	states, cancel := client.SubscribeState(16)
	defer cancel()
	tick := time.NewTicker(nodeRescanEvery)
	defer tick.Stop()
	wasConnected := client.State().Connected
	for {
		select {
		case <-ctx.Done():
			return
		case st, ok := <-states:
			if !ok {
				return
			}
			up := st.Connected && !wasConnected
			wasConnected = st.Connected
			if !up {
				continue
			}
		case <-rescan:
		case <-tick.C:
			if !client.State().Connected {
				continue
			}
		}
		if !sleep(ctx, nodeRescanDelay) {
			return
		}
		// Right after a restart rpt may not have its nodes loaded yet.
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 && !sleep(ctx, 5*time.Second) {
				return
			}
			if refreshNodes(ctx, rc, mon, logger) {
				break
			}
		}
	}
}

// refreshNodes re-reads the local nodes into mon; false means it could not.
func refreshNodes(ctx context.Context, rc *rpt.Client, mon *rpt.Monitor, logger *log.Logger) bool {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	nodes, err := rc.LocalNodes(cctx)
	cancel()
	if err != nil || len(nodes) == 0 {
		if err == nil {
			err = errors.New("no local nodes configured")
		}
		if ctx.Err() == nil {
			logger.Printf("ami: re-reading local nodes: %v (keeping the current set)", err)
		}
		return false
	}
	added, removed := mon.SetNodes(nodes)
	if len(added)+len(removed) > 0 {
		logger.Printf("monitoring local nodes %v (added %v, removed %v)", mon.Nodes(), added, removed)
	}
	return true
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// lateAMI lets the API and system collector exist before the AMI client does
// (credentials may need resolving, or manager.conf may not be readable yet).
type lateAMI struct {
	mu sync.RWMutex
	c  *ami.Client
}

func (l *lateAMI) set(c *ami.Client) {
	l.mu.Lock()
	l.c = c
	l.mu.Unlock()
}

func (l *lateAMI) get() *ami.Client {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.c
}

func (l *lateAMI) State() ami.State {
	if c := l.get(); c != nil {
		return c.State()
	}
	return ami.State{Err: errors.New("not configured yet")}
}

func (l *lateAMI) Action(ctx context.Context, name string, h ...ami.Header) (*ami.Message, error) {
	if c := l.get(); c != nil {
		return c.Action(ctx, name, h...)
	}
	return nil, ami.ErrNotConnected
}

func (l *lateAMI) Command(ctx context.Context, cmd string) ([]string, error) {
	if c := l.get(); c != nil {
		return c.Command(ctx, cmd)
	}
	return nil, ami.ErrNotConnected
}

// seedDemoSchedules adds two example schedules to an empty demo database: a
// daily 15 minute window a few minutes after the next quarter hour, and a
// daily disconnect after it, so the Schedules tab has something to show.
func seedDemoSchedules(st *store.Store, now time.Time) {
	if l, _ := st.ListSchedules(); len(l) != 0 {
		return
	}
	start := now.Truncate(15 * time.Minute).Add(15*time.Minute + 3*time.Minute)
	end := start.Add(15 * time.Minute)
	off := end.Add(5 * time.Minute)
	all := []int{0, 1, 2, 3, 4, 5, 6}
	clock := func(t time.Time) string { return t.Format("15:04") }
	for _, x := range []schedules.Schedule{
		{Name: "Demo net", Enabled: true, Node: "2006", Target: "2000", Action: schedules.ActionConnect, Mode: schedules.ModeTransceive,
			Days: all, Start: clock(start), End: clock(end)},
		{Name: "Tidy up", Enabled: true, Node: "2006", Target: "50241", Action: schedules.ActionDisconnect,
			Days: all, Start: clock(off)},
	} {
		if err := x.Normalize(); err != nil {
			continue
		}
		_, _ = st.AddSchedule(x)
	}
}
