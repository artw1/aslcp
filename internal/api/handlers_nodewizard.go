package api

import (
	"net/http"

	"github.com/artw1/aslcp/internal/nodecfg"
	"github.com/artw1/aslcp/internal/store"
)

// Add-node and change-node-number wizards: plan and apply. Apply runs the
// same ordered runner as the setup wizard (runWizard in nodesetup_run.go);
// rollback is the setup wizard's (POST /api/config/setup/rollback).

func (s *Server) nodeWizardRoutes() {
	m := s.mux
	m.HandleFunc("POST /api/config/wizard/add-node/plan", s.authed(store.RoleAdmin, s.handleAddNodePlan))
	m.HandleFunc("POST /api/config/wizard/add-node/apply", s.authed(store.RoleAdmin, s.handleAddNodeApply))
	m.HandleFunc("POST /api/config/wizard/renumber/plan", s.authed(store.RoleAdmin, s.handleRenumberPlan))
	m.HandleFunc("POST /api/config/wizard/renumber/apply", s.authed(store.RoleAdmin, s.handleRenumberApply))
}

type addNodeBody struct {
	nodecfg.AddNodeRequest
	Base    map[string]string `json:"base"`
	Message string            `json:"message"`
}

type renumberBody struct {
	nodecfg.RenumberRequest
	Base    map[string]string `json:"base"`
	Message string            `json:"message"`
}

func (s *Server) handleAddNodePlan(w http.ResponseWriter, r *http.Request, _ store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	var in addNodeBody
	if !decodeMax(w, r, &in, false, maxConfigBody) {
		return
	}
	plan, err := s.cfgWorld(r.Context(), svc).PlanAddNode(in.AddNodeRequest)
	if err != nil {
		s.setupError(w, err)
		return
	}
	writeJSON(w, 200, setupPlanResult{Plan: plan})
}

func (s *Server) handleAddNodeApply(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	var in addNodeBody
	if !decodeMax(w, r, &in, false, maxConfigBody) {
		return
	}
	plan, err := s.cfgWorld(r.Context(), svc).PlanAddNode(in.AddNodeRequest)
	if err != nil {
		s.setupError(w, err)
		return
	}
	s.runWizard(w, r, u, svc, wizardSpec{
		action: "config.addnode", logName: "add node", defaultMsg: "Add node " + plan.Node, plan: plan, base: in.Base, message: in.Message, added: true,
	})
}

func (s *Server) handleRenumberPlan(w http.ResponseWriter, r *http.Request, _ store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	var in renumberBody
	if !decodeMax(w, r, &in, false, maxConfigBody) {
		return
	}
	plan, err := s.cfgWorld(r.Context(), svc).PlanRenumber(in.RenumberRequest)
	if err != nil {
		s.setupError(w, err)
		return
	}
	writeJSON(w, 200, setupPlanResult{Plan: plan})
}

func (s *Server) handleRenumberApply(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	var in renumberBody
	if !decodeMax(w, r, &in, false, maxConfigBody) {
		return
	}
	plan, err := s.cfgWorld(r.Context(), svc).PlanRenumber(in.RenumberRequest)
	if err != nil {
		s.setupError(w, err)
		return
	}
	s.runWizard(w, r, u, svc, wizardSpec{
		action: "config.renumber", logName: "renumber", defaultMsg: "Change node number " + plan.OldNode + " to " + plan.Node, plan: plan, base: in.Base, message: in.Message,
		savenode: in.Savenode, password: in.Password, allmon: true,
	})
}
