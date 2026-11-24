package api

import (
	"context"
	"net/http"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// The audio settings (AGC and gains, shared by all modes) and the level meter
// of the bridge (docs/DV.md "Audio level and AGC", docs/API.md).

// dvMeterTTL is how long one reading of the engine is shared between callers:
// the card polls about once a second, several viewers share one engine call.
const dvMeterTTL = 400 * time.Millisecond

// DVMeter is the reply of GET /api/dv/meter. Running is false when the engine
// does not answer.
type DVMeter struct {
	Running bool             `json:"running"`
	Audio   *ctl.AudioStatus `json:"audio,omitempty"`
}

func (s *Server) handleDVMeter(w http.ResponseWriter, r *http.Request, _ store.User) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.dvMeter(r.Context()))
}

func (s *Server) dvMeter(ctx context.Context) DVMeter {
	d := s.dvs
	if d == nil || s.d.DVCtl == nil {
		return DVMeter{}
	}
	now := d.now()
	d.mu.Lock()
	if d.meter.fresh(now, dvMeterTTL) {
		v, ok := d.meter.v, d.meter.ok
		d.mu.Unlock()
		return DVMeter{Running: ok, Audio: v}
	}
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	st, err := s.d.DVCtl.Status(ctx)
	v, ok := st.Audio, err == nil && st.Audio != nil
	if !ok {
		v = nil
	}
	d.mu.Lock()
	d.meter = dvCache[*ctl.AudioStatus]{v: v, ok: ok, at: now}
	d.mu.Unlock()
	return DVMeter{Running: ok, Audio: v}
}

func (s *Server) handleDVAudioGet(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	rep, err := s.d.DVEngine.DVNetworkGet(r.Context())
	if err != nil {
		s.dvError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, rep.Audio)
}

func (s *Server) handleDVAudioPut(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	b := dvpanel.DefaultAudio() // a field left out keeps its default
	if !decode(w, r, &b, false) {
		return
	}
	if err := dvpanel.ValidateAudio(b); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_field", err.Error())
		return
	}
	rep, err := s.d.DVEngine.DVNetworkSet(r.Context(), helper.DVNetworkRequest{Audio: &b})
	if err != nil {
		s.audit(u, "dv.audio.refused", nil)
		s.dvError(w, err)
		return
	}
	s.audit(u, "dv.audio", nil)
	s.d.Logger.Printf("dv.audio changed by %s", u.Username)
	applied, why := s.dvApplyToEngine(r.Context(), rep.Rendered)
	writeJSON(w, 200, struct {
		dvpanel.AudioState
		Applied    bool   `json:"applied"`
		ApplyError string `json:"applyError,omitempty"`
	}{rep.Audio, applied, why})
}
