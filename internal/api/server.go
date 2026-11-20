// Package api implements the REST + SSE interface described in docs/API.md.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/activity"
	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/auth"
	"github.com/artw1/aslcp/internal/backupsvc"
	"github.com/artw1/aslcp/internal/config"
	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/diag"
	"github.com/artw1/aslcp/internal/dvswitch"
	"github.com/artw1/aslcp/internal/favstats"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/nodelist"
	"github.com/artw1/aslcp/internal/public"
	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/schedules"
	"github.com/artw1/aslcp/internal/store"
	"github.com/artw1/aslcp/internal/susb"
	"github.com/artw1/aslcp/internal/system"
	"github.com/artw1/aslcp/internal/updates"
)

// Deps are the server's collaborators.
type Deps struct {
	Auth     *auth.Service
	Store    *store.Store
	AMI      system.AMI // rpt.API + connection State
	Nodelist *nodelist.DB
	System   *system.Collector
	// Updates reports and applies package updates (may be nil: unavailable).
	Updates *updates.Manager
	// Power restarts the panel and reboots the machine through the helper
	// (nil: 503 updates_unavailable).
	Power Power
	// SysHost is the helper behind the System tab: hostname and time, logs
	// and services, network status (nil: 503 system_unavailable).
	SysHost SystemHost
	// DVSwitch reports, installs and removes DVSwitch (nil: 503
	// dvswitch_unavailable).
	DVSwitch DVSwitch
	// DVResume holds the last tune per mode and the resume setting (nil: 503
	// dvswitch_unavailable).
	DVResume DVResume
	// DVInstalled says whether DVSwitch is on this node, without asking the
	// helper (nil: assume it may be).
	DVInstalled func() bool
	// DVBridgeNode finds the local node that uses a USRP channel (rpt.conf,
	// read-only; nil: never found).
	DVBridgeNode func(ctx context.Context) (string, bool)
	// ConfigEditor is the configuration editing service (nil: 503
	// config_unavailable).
	ConfigEditor *configsvc.Service
	Version      string
	// UI serves the embedded SPA for non-/api paths (may be nil).
	UI http.Handler
	// AllowedOrigins are extra CSRF-allowed Origins (the Vite dev server).
	AllowedOrigins []string
	// AllScanDirs are searched for favorites*.ini.
	AllScanDirs []string
	Logger      *log.Logger
	// Diagnostics (GET /api/diagnostics): the in-memory log, the effective
	// configuration, the helper's firewall status (nil: n/a) and platform
	// file locations (nil: the real ones; tests inject others).
	Log           *diag.Ring
	Config        config.Config
	ConfigPath    string
	Firewall      func(ctx context.Context) (helper.Firewall, error)
	PlatformPaths *diag.PlatformPaths
	// PublicDefaultEnabled turns the public status view on when the admin
	// never chose (demo mode).
	PublicDefaultEnabled bool
	// Backup is the backup and restore service (nil: 503
	// backup_unavailable). Restart ends the process for a restart after a
	// restore (nil: no restart, demo mode).
	Backup  *backupsvc.Service
	Restart func()
	// Tuning is the SimpleUSB tuning service (nil: 503 radio_unavailable).
	// New wires its node state check to the monitor.
	Tuning *susb.Service
	// RescanNodes asks for the local node list to be re-read after the panel
	// reloaded or restarted Asterisk (nil: no-op).
	RescanNodes func()
	// FavStats is the favorites activity scan (nil: feature unavailable).
	// New wires its sources and update broadcast.
	FavStats *favstats.Service
	// Schedules runs the scheduled connects (nil: feature unavailable, 503
	// schedules_unavailable). New wires its link controller (unless set) and
	// its change broadcast.
	Schedules *schedules.Engine
	// DVLink keeps the permanent link of the private bridge node (nil: the
	// link features are off). New wires its Linker (unless set), Store (unless
	// set) and its change notification; Run starts it.
	DVLink *dvswitch.LinkReconciler
}

// Server is the HTTP API.
type Server struct {
	d   Deps
	rpt *rpt.Client
	hub *hub
	pub *public.Service
	rec *activity.Recorder
	mux *http.ServeMux

	// Intervals (exported for tests).
	FastInterval   time.Duration // monitor poll while SSE clients are connected
	SlowInterval   time.Duration // monitor poll otherwise
	PingInterval   time.Duration
	SystemInterval time.Duration
	// LinkGoneWait bounds how long an exclusive connect waits for each
	// disconnected link to disappear before connecting anyway (a permanent
	// link never goes away).
	LinkGoneWait time.Duration
	// AsteriskBackWait bounds how long a flow that restarted Asterisk (the
	// DVSwitch bridge node apply and rollback) waits for it to answer again,
	// polling every AsteriskBackPoll.
	AsteriskBackWait time.Duration
	AsteriskBackPoll time.Duration

	schedMu sync.Mutex // serialises schedule saves (overlap check + write)

	secretsMu sync.Mutex
	secrets   []string
	platOnce  sync.Once
	plat      diag.Platform

	monMu sync.RWMutex
	mon   *rpt.Monitor

	// term is the browser terminal: tickets, limits, open sessions.
	term termState

	dvs *dvSnapper // DVSwitch snapshot and its change events
	// dvUndo is what the last private-node apply changed (for its rollback).
	dvUndo   *dvPrivateUndo
	dvUndoMu sync.Mutex
	// dvAdoptMu serializes the take-over of a bridge node.
	dvAdoptMu sync.Mutex

	stop     chan struct{}
	stopOnce sync.Once

	// instance identifies this process (random, new at every start) so the UI
	// can tell a restarted server from the old one.
	instance string
}

// New builds the server.
func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = log.Default()
	}
	ib := make([]byte, 8)
	_, _ = rand.Read(ib)
	s := &Server{
		instance: hex.EncodeToString(ib),
		d:        d, rpt: rpt.New(d.AMI), mux: http.NewServeMux(),
		FastInterval: time.Second, SlowInterval: 10 * time.Second,
		PingInterval: 15 * time.Second, SystemInterval: 10 * time.Second,
		LinkGoneWait:     3 * time.Second,
		AsteriskBackWait: 90 * time.Second, AsteriskBackPoll: time.Second,
		stop: make(chan struct{}),
	}
	s.hub = newHub(func(int) { s.updateInterval(0) })
	if d.Updates != nil && d.Updates.OS != nil {
		d.Updates.OS.OnChange = s.broadcastUpdates
	}
	var pst public.Store
	if d.Store != nil {
		pst = d.Store
	}
	var lk public.Lookup
	if d.Nodelist != nil {
		lk = d.Nodelist
	}
	s.pub = public.New(public.Options{
		Store: pst, Lookup: lk, Logger: d.Logger, DefaultEnabled: d.PublicDefaultEnabled,
		OnClients: func(int) { s.updateInterval(0) },
	})
	if d.Store != nil {
		s.rec = activity.New(d.Store, d.Logger)
	} else {
		s.rec = activity.New(noActivity{}, d.Logger)
	}
	s.rec.OnRecord = s.pub.ActivityRecorded
	s.initFavStats()
	s.initSchedules()
	s.initDVSwitch()
	s.initDVLink()
	s.routes()
	return s
}

// SetCommandGap sets the minimum spacing between app_rpt commands to one
// node (see rpt.Client.MinGap); tests use a small value.
func (s *Server) SetCommandGap(d time.Duration) { s.rpt.MinGap = d }

// Run starts background loops (system pushes) until ctx is done.
func (s *Server) Run(ctx context.Context) {
	go s.systemLoop(ctx)
	go s.rec.Run(ctx)
	go s.terminalLoop(ctx)
	if s.d.FavStats != nil {
		go s.d.FavStats.Run(ctx)
	}
	if s.d.Schedules != nil {
		go s.d.Schedules.Run(ctx)
	}
	if s.d.DVLink != nil {
		go s.d.DVLink.Run(ctx)
	}
	<-ctx.Done()
	s.Shutdown()
}

// Shutdown closes SSE streams (so http.Server.Shutdown can finish).
func (s *Server) Shutdown() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.hub.closeAll()
	s.pub.Close()
}

// Public returns the public status service (for tests and wiring).
func (s *Server) Public() *public.Service { return s.pub }

// Handler returns the full HTTP handler (API, UI, security headers).
func (s *Server) Handler() http.Handler { return s.secure(s.mux) }

// Clients returns the number of SSE clients.
func (s *Server) Clients() int { return s.hub.count() }

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":"encoding error","code":"internal"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

func noContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

func (s *Server) internal(w http.ResponseWriter, err error) {
	s.d.Logger.Printf("internal error: %v", err)
	writeErr(w, http.StatusInternalServerError, "internal", "internal error")
}

const maxBody = 64 << 10

// decode reads a JSON body into v; an empty body is allowed when optional.
func decode(w http.ResponseWriter, r *http.Request, v any, optional bool) bool {
	return decodeMax(w, r, v, optional, maxBody)
}

func decodeMax(w http.ResponseWriter, r *http.Request, v any, optional bool, max int64) bool {
	body := http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) && optional {
			return true
		}
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid id")
		return 0, false
	}
	return id, true
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, u store.User)

// authed wraps a handler with session auth and a minimum role.
func (s *Server) authed(min string, h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.d.Auth.Authenticate(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if auth.RoleRank(u.Role) < auth.RoleRank(min) {
			writeErr(w, http.StatusForbidden, "forbidden", "insufficient permissions")
			return
		}
		h(w, r, u)
	}
}

func (s *Server) amiUp() bool { return s.d.AMI != nil && s.d.AMI.State().Connected }

var errAMIDown = ami.ErrNotConnected

// ---------------------------------------------------------------- routing

func (s *Server) routes() {
	m := s.mux
	// setup & session
	m.HandleFunc("GET /api/setup", s.handleSetupGet)
	m.HandleFunc("POST /api/setup", s.handleSetupPost)
	m.HandleFunc("POST /api/login", s.handleLogin)
	m.HandleFunc("POST /api/logout", s.handleLogout)
	m.HandleFunc("GET /api/session", s.authed(store.RoleViewer, s.handleSession))
	m.HandleFunc("POST /api/password", s.authed(store.RoleViewer, s.handlePassword))
	// users
	m.HandleFunc("GET /api/users", s.authed(store.RoleAdmin, s.handleUsersList))
	m.HandleFunc("POST /api/users", s.authed(store.RoleAdmin, s.handleUsersCreate))
	m.HandleFunc("PUT /api/users/{id}", s.authed(store.RoleAdmin, s.handleUsersUpdate))
	m.HandleFunc("DELETE /api/users/{id}", s.authed(store.RoleAdmin, s.handleUsersDelete))
	// nodes
	m.HandleFunc("GET /api/nodes", s.authed(store.RoleViewer, s.handleNodes))
	m.HandleFunc("POST /api/nodes/{node}/connect", s.authed(store.RoleOperator, s.handleConnect))
	m.HandleFunc("POST /api/nodes/{node}/disconnect", s.authed(store.RoleOperator, s.handleDisconnect))
	m.HandleFunc("POST /api/nodes/{node}/disconnect-all", s.authed(store.RoleOperator, s.handleDisconnectAll))
	m.HandleFunc("GET /api/nodes/{node}/stats", s.authed(store.RoleViewer, s.handleNodeStats))
	// favorites
	m.HandleFunc("GET /api/favorites", s.authed(store.RoleViewer, s.handleFavList))
	m.HandleFunc("GET /api/favorites/stats", s.authed(store.RoleViewer, s.handleFavStats))
	m.HandleFunc("PUT /api/favorites/scan", s.authed(store.RoleOperator, s.handleFavScan))
	m.HandleFunc("POST /api/favorites", s.authed(store.RoleOperator, s.handleFavCreate))
	m.HandleFunc("POST /api/favorites/reorder", s.authed(store.RoleOperator, s.handleFavReorder))
	m.HandleFunc("GET /api/favorites/import/allscan", s.authed(store.RoleOperator, s.handleImportPreview))
	m.HandleFunc("POST /api/favorites/import/allscan", s.authed(store.RoleOperator, s.handleImport))
	m.HandleFunc("PUT /api/favorites/{id}", s.authed(store.RoleOperator, s.handleFavUpdate))
	m.HandleFunc("DELETE /api/favorites/{id}", s.authed(store.RoleOperator, s.handleFavDelete))
	// schedules
	m.HandleFunc("GET /api/schedules", s.authed(store.RoleViewer, s.handleSchedList))
	m.HandleFunc("POST /api/schedules", s.authed(store.RoleOperator, s.handleSchedCreate))
	m.HandleFunc("PUT /api/schedules/enabled", s.authed(store.RoleOperator, s.handleSchedEnabled))
	m.HandleFunc("PUT /api/schedules/settings", s.authed(store.RoleOperator, s.handleSchedSettings))
	m.HandleFunc("PUT /api/schedules/{id}", s.authed(store.RoleOperator, s.handleSchedUpdate))
	m.HandleFunc("DELETE /api/schedules/{id}", s.authed(store.RoleOperator, s.handleSchedDelete))
	// settings
	m.HandleFunc("GET /api/settings", s.authed(store.RoleViewer, s.handleSettingsGet))
	// lookup, nodelist, system, events
	m.HandleFunc("GET /api/lookup/{node}", s.authed(store.RoleViewer, s.handleLookup))
	m.HandleFunc("GET /api/lookup", s.authed(store.RoleViewer, s.handleSearch))
	m.HandleFunc("GET /api/nodelist", s.authed(store.RoleViewer, s.handleNodelist))
	m.HandleFunc("POST /api/nodelist/refresh", s.authed(store.RoleAdmin, s.handleNodelistRefresh))
	m.HandleFunc("GET /api/system", s.authed(store.RoleViewer, s.handleSystem))
	m.HandleFunc("POST /api/system/restart-asterisk", s.authed(store.RoleAdmin, s.handleRestartAsterisk))
	m.HandleFunc("GET /api/about", s.authed(store.RoleViewer, s.handleAbout))
	m.HandleFunc("GET /api/diagnostics", s.authed(store.RoleAdmin, s.handleDiagnostics))
	s.configRoutes()
	s.backupRoutes()
	s.radioRoutes()
	s.dvswitchRoutes()
	s.systemRoutes()
	m.HandleFunc("GET /api/updates", s.authed(store.RoleAdmin, s.handleUpdatesGet))
	m.HandleFunc("POST /api/updates/check", s.authed(store.RoleAdmin, s.handleUpdatesCheck))
	m.HandleFunc("POST /api/updates/apply", s.authed(store.RoleAdmin, s.handleUpdatesApply))
	m.HandleFunc("PUT /api/updates/channel", s.authed(store.RoleAdmin, s.handleUpdatesChannel))
	m.HandleFunc("POST /api/updates/os/check", s.authed(store.RoleAdmin, s.handleOSCheck))
	m.HandleFunc("POST /api/updates/os/apply", s.authed(store.RoleAdmin, s.handleOSApply))
	m.HandleFunc("POST /api/system/reboot", s.authed(store.RoleAdmin, s.handleReboot))
	m.HandleFunc("POST /api/system/restart-aslcp", s.authed(store.RoleAdmin, s.handleRestartASLCP))
	m.HandleFunc("GET /api/events", s.authed(store.RoleViewer, s.handleEvents))
	// public status view: no auth, 404 unless enabled by an admin
	m.HandleFunc("GET /api/public/info", func(w http.ResponseWriter, r *http.Request) {
		_, ok := s.d.Auth.Authenticate(r)
		s.pub.Info(w, r, ok)
	})
	m.HandleFunc("GET /api/public/status", s.pub.Status)
	m.HandleFunc("GET /api/public/events", s.pub.Events)
	m.HandleFunc("GET /api/public", s.authed(store.RoleAdmin, s.handlePublicGet))
	m.HandleFunc("PUT /api/public", s.authed(store.RoleAdmin, s.handlePublicPut))
	m.HandleFunc("GET /api/activity", s.authed(store.RoleViewer, s.handleActivity))
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	if s.d.UI != nil {
		m.Handle("/", s.d.UI)
	}
}

// secure applies security headers and CSRF enforcement for /api.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Only /embed (the chrome-less public view) can be framed, and only
		// while the public page and "allow embedding" are both on. Every other
		// path, the login page included, is never framable.
		frame := "'none'"
		embed := r.URL.Path == "/embed" || r.URL.Path == "/embed/"
		if embed {
			if c := s.pub.Settings(); c.Enabled && c.AllowEmbedding {
				frame = "*"
			}
		}
		// connect-src names this host's ws:// and wss:// explicitly: 'self' is
		// not read as covering WebSocket by every browser (older Safari), and
		// the terminal needs one. Nothing wider than this host is allowed.
		connect := "connect-src 'self'"
		if hh := cspHost(r.Host); hh != "" {
			connect += " ws://" + hh + " wss://" + hh
		}
		h.Set("Content-Security-Policy", "default-src 'self'; "+connect+"; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors "+frame+"; base-uri 'self'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if frame == "'none'" {
			h.Set("X-Frame-Options", "DENY")
		}
		if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
			h.Set("Cache-Control", "no-store")
			h.Set("X-ASLCP-Instance", s.instance)
			if reason := auth.CheckCSRF(r, s.d.AllowedOrigins); reason != "" {
				writeErr(w, http.StatusForbidden, "csrf", reason)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// noActivity discards activity when there is no store (tests).
type noActivity struct{}

func (noActivity) AddActivity(store.Activity) error            { return nil }
func (noActivity) PruneActivity(time.Time, int) (int64, error) { return 0, nil }
