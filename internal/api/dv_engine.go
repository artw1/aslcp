package api

import (
	"context"
	"errors"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/helper"
)

// DVEngine is the part of the root helper that manages digital modes: the
// install job, the engine config, the record of the private node (docs/DV.md).
// *helper.Client implements it.
type DVEngine interface {
	DVStatus(ctx context.Context) (helper.DVStatus, error)
	DVJob(ctx context.Context) (helper.DVJob, error)
	DVEnable(ctx context.Context) (helper.DVStatus, error)
	DVDisable(ctx context.Context) (helper.DVStatus, error)
	// DVConfig renders the engine's config file.
	DVConfig(ctx context.Context) (helper.DVConfigResult, error)
	// DVNetworkGet and DVNetworkSet read and change the network settings and the
	// identity (the helper renders the engine config).
	DVNetworkGet(ctx context.Context) (helper.DVNetworkReply, error)
	DVNetworkSet(ctx context.Context, req helper.DVNetworkRequest) (helper.DVNetworkReply, error)
	// DVRestart restarts the engine unit (the fallback of a reload).
	DVRestart(ctx context.Context) (helper.DVStatus, error)
	DVPrivateGet(ctx context.Context) (helper.DVPrivateReply, error)
	DVPrivateSet(ctx context.Context, rec helper.DVPrivateRecord) (helper.DVPrivateReply, error)
	DVPrivateClear(ctx context.Context) (helper.DVPrivateReply, error)
}

// DVCtl is the engine's control socket (*ctl.Client implements it).
type DVCtl interface {
	Status(ctx context.Context) (ctl.Status, error)
	Reload(ctx context.Context) (ctl.Status, error)
	Mode(ctx context.Context, a ctl.ModeArgs) (ctl.Status, error)
	Tune(ctx context.Context, a ctl.TuneArgs) (ctl.Status, error)
	Unlink(ctx context.Context, a ctl.UnlinkArgs) (ctl.Status, error)
	Subscribe(ctx context.Context, first func(ctl.Status), fn func(ctl.Event)) error
	Path() string
}

// dvSetup is what the private node flow reads: the record of what ASLCP
// changed and the USRP ports of the bridge node as rpt.conf has them.
type dvSetup struct {
	Private       *helper.DVPrivateRecord
	USRP          struct{ RxPort, TxPort int }
	ReservedPorts []int
}

// dvSetupState reads the record and the ports. A missing record is not an
// error.
func (s *Server) dvSetupState(ctx context.Context) (dvSetup, error) {
	var v dvSetup
	if s.d.DVEngine == nil {
		return v, errDVUnavailable
	}
	rep, err := s.d.DVEngine.DVPrivateGet(ctx)
	if err != nil {
		return v, err
	}
	v.Private = rep.Record
	v.USRP.RxPort, v.USRP.TxPort = dvpanel.DefaultRxPort, dvpanel.DefaultTxPort
	node := ""
	if v.Private != nil {
		node = v.Private.Node
	} else if n, ok := s.dvDetectedBridge(ctx); ok {
		node = n
	}
	if node != "" && s.d.ConfigEditor != nil {
		if tree, err := s.d.ConfigEditor.Tree(ctx, dvRptFile); err == nil {
			for _, c := range dvpanel.USRPChannels(tree) {
				if c.Node == node && c.RxPort != 0 && c.TxPort != 0 {
					v.USRP.RxPort, v.USRP.TxPort = c.RxPort, c.TxPort
				}
			}
		}
	}
	return v, nil
}

// dvRenderConfig has the helper write the engine's config.
func (s *Server) dvRenderConfig(ctx context.Context) error {
	if s.d.DVEngine == nil {
		return errDVUnavailable
	}
	_, err := s.d.DVEngine.DVConfig(ctx)
	return err
}

// dvReloadEngine tells the engine to re-read its config; when it does not
// answer the helper restarts the unit.
func (s *Server) dvReloadEngine(ctx context.Context) error {
	if s.d.DVCtl != nil {
		st, err := s.d.DVCtl.Reload(ctx)
		if err == nil {
			s.dvs.setStatus(st)
			return nil
		}
		var ce *ctl.Error
		if errors.As(err, &ce) && ce.Code == ctl.CodeBadConfig {
			return err // restarting would not help
		}
	}
	if s.d.DVEngine == nil {
		return errDVUnavailable
	}
	if _, err := s.d.DVEngine.DVRestart(ctx); err != nil {
		return err
	}
	// give the unit a moment to open its socket before the health check
	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
	}
	return nil
}
