package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/store"
	"github.com/artw1/aslcp/internal/susb"
)

// radioTimeout bounds one request.
const radioTimeout = 30 * time.Second

func (s *Server) radioRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/radio", s.authed(store.RoleAdmin, s.handleRadioList))
	m.HandleFunc("GET /api/radio/{dev}", s.authed(store.RoleAdmin, s.handleRadioGet))
	m.HandleFunc("PUT /api/radio/{dev}/params", s.authed(store.RoleAdmin, s.handleRadioParams))
	m.HandleFunc("GET /api/radio/{dev}/meter", s.authed(store.RoleAdmin, s.handleRadioMeter))
	m.HandleFunc("POST /api/radio/{dev}/echo", s.authed(store.RoleAdmin, s.handleRadioEcho))
	m.HandleFunc("POST /api/radio/{dev}/save", s.authed(store.RoleAdmin, s.handleRadioSave))
	m.HandleFunc("POST /api/radio/{dev}/revert", s.authed(store.RoleAdmin, s.handleRadioRevert))
}

func (s *Server) radioLinks(node string) []string {
	out := []string{}
	m := s.monitor()
	if m == nil {
		return out
	}
	if st, ok := m.Status(node); ok {
		for _, l := range st.Links {
			out = append(out, l.Node)
		}
	}
	return out
}

func (s *Server) radioSvc(w http.ResponseWriter) *susb.Service {
	if s.d.Tuning == nil {
		writeErr(w, http.StatusServiceUnavailable, "radio_unavailable", "radio tuning is not available")
		return nil
	}
	return s.d.Tuning
}

// radioErr maps a susb error to a response.
func (s *Server) radioErr(w http.ResponseWriter, err error) {
	var e *susb.Error
	switch {
	case errors.As(err, &e):
		st := http.StatusBadGateway
		switch e.Code {
		case susb.CodeInvalid:
			st = http.StatusBadRequest
		case susb.CodeUnknownDevice:
			st = http.StatusNotFound
		case susb.CodeEchoOn, susb.CodeNoBaseline:
			st = http.StatusConflict
		case susb.CodeUnavailable, susb.CodeNoDriver:
			st = http.StatusServiceUnavailable
		case susb.CodeUnsupported:
			st = http.StatusNotImplemented
		}
		writeErr(w, st, e.Code, e.Msg)
	case errors.Is(err, ami.ErrNotConnected):
		writeErr(w, http.StatusServiceUnavailable, "ami_down", "not connected to Asterisk")
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "radio_unavailable", "the radio interface did not answer in time")
	default:
		s.internal(w, err)
	}
}

func (s *Server) radioCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), radioTimeout)
}

// RadioDeviceJSON is one device of GET /api/radio.
type RadioDeviceJSON struct {
	susb.Device
	Links []string `json:"links"`
}

func (s *Server) handleRadioList(w http.ResponseWriter, r *http.Request, _ store.User) {
	out := struct {
		Available bool              `json:"available"`
		Reason    string            `json:"reason,omitempty"`
		Devices   []RadioDeviceJSON `json:"devices"`
	}{Devices: []RadioDeviceJSON{}}
	if s.d.Tuning == nil {
		out.Reason = "radio tuning is not available"
		writeJSON(w, 200, out)
		return
	}
	ctx, cancel := s.radioCtx(r)
	defer cancel()
	devs, err := s.d.Tuning.Devices(ctx)
	if err != nil {
		var e *susb.Error
		if errors.As(err, &e) || errors.Is(err, ami.ErrNotConnected) || errors.Is(err, context.DeadlineExceeded) {
			out.Reason = err.Error()
			writeJSON(w, 200, out)
			return
		}
		s.internal(w, err)
		return
	}
	for _, d := range devs {
		out.Devices = append(out.Devices, RadioDeviceJSON{Device: d, Links: s.radioLinks(d.Node)})
	}
	out.Available = len(out.Devices) > 0
	if !out.Available {
		out.Reason = "no SimpleUSB device is configured"
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleRadioGet(w http.ResponseWriter, r *http.Request, _ store.User) {
	svc := s.radioSvc(w)
	if svc == nil {
		return
	}
	ctx, cancel := s.radioCtx(r)
	defer cancel()
	st, err := svc.Read(ctx, r.PathValue("dev"))
	if err != nil {
		s.radioErr(w, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleRadioParams(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.radioSvc(w)
	if svc == nil {
		return
	}
	var p susb.Patch
	if !decode(w, r, &p, false) {
		return
	}
	ctx, cancel := s.radioCtx(r)
	defer cancel()
	dev := r.PathValue("dev")
	res, err := svc.Apply(ctx, dev, p)
	if err != nil {
		s.radioErr(w, err)
		return
	}
	fields := []string{}
	for _, a := range res.Applied {
		fields = append(fields, a.Field)
	}
	s.audit(u, "radio.params", map[string]any{"device": dev, "fields": fields, "failed": len(res.Errors)})
	writeJSON(w, 200, res)
}

func (s *Server) handleRadioMeter(w http.ResponseWriter, r *http.Request, _ store.User) {
	svc := s.radioSvc(w)
	if svc == nil {
		return
	}
	ctx, cancel := s.radioCtx(r)
	defer cancel()
	m, err := svc.Meter(ctx, r.PathValue("dev"))
	if err != nil {
		s.radioErr(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *Server) handleRadioEcho(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.radioSvc(w)
	if svc == nil {
		return
	}
	var in struct {
		On bool `json:"on"`
	}
	if !decode(w, r, &in, false) {
		return
	}
	ctx, cancel := s.radioCtx(r)
	defer cancel()
	dev := r.PathValue("dev")
	es, err := svc.SetEcho(ctx, dev, in.On)
	if err != nil {
		s.radioErr(w, err)
		return
	}
	d := map[string]any{"device": dev, "on": in.On}
	if in.On {
		d["links"] = s.deviceLinks(ctx, dev)
	}
	s.audit(u, "radio.echo", d)
	writeJSON(w, 200, es)
}

// deviceLinks lists the nodes linked to the node of dev (for the audit).
func (s *Server) deviceLinks(ctx context.Context, dev string) []string {
	devs, err := s.d.Tuning.Devices(ctx)
	if err != nil {
		return []string{}
	}
	for _, d := range devs {
		if d.Name == dev {
			return s.radioLinks(d.Node)
		}
	}
	return []string{}
}

func (s *Server) handleRadioSave(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.radioSvc(w)
	if svc == nil {
		return
	}
	ctx, cancel := s.radioCtx(r)
	defer cancel()
	dev := r.PathValue("dev")
	res, err := svc.Save(ctx, dev, u.Username)
	if err != nil {
		s.radioErr(w, err)
		return
	}
	s.audit(u, "radio.save", map[string]any{"device": dev, "changed": res.Changed, "commit": res.Commit})
	writeJSON(w, 200, res)
}

func (s *Server) handleRadioRevert(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.radioSvc(w)
	if svc == nil {
		return
	}
	ctx, cancel := s.radioCtx(r)
	defer cancel()
	dev := r.PathValue("dev")
	res, err := svc.Revert(ctx, dev)
	if err != nil {
		s.radioErr(w, err)
		return
	}
	s.audit(u, "radio.revert", map[string]any{"device": dev, "failed": len(res.Errors)})
	writeJSON(w, 200, res)
}
