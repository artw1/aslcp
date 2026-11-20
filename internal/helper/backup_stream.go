package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"time"
)

// streamTimeout bounds a backup-create or backup-restore request.
const streamTimeout = 20 * time.Minute

// handleStream serves backup-create and backup-restore (the request has been
// read and the peer authorized).
func (s *Server) handleStream(ctx context.Context, conn net.Conn, in io.Reader, req Request, uid uint32) {
	_ = conn.SetDeadline(time.Now().Add(streamTimeout))
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), streamTimeout)
	defer cancel()
	reply := func(v any) {
		_ = json.NewEncoder(conn).Encode(v)
	}
	switch req.Op {
	case OpDiagCollect:
		s.handleDiagStream(cctx, conn, req, uid)
	case OpBackupCreate:
		br := req.Backup
		if br == nil {
			br = &BackupRequest{}
		}
		bw := bufio.NewWriterSize(conn, 64<<10)
		sent, err := s.Ops.BackupCreate(cctx, br, bw)
		if err != nil {
			s.logf("uid %d: %s: error: %v", uid, req.Op, err)
			if !sent {
				reply(errorResponse{Error: err.Error()})
			}
			// After the header: no flush of the final tar blocks, the
			// truncated stream tells the client.
			_ = bw.Flush()
			return
		}
		if err := bw.Flush(); err != nil {
			s.logf("uid %d: %s: write: %v", uid, req.Op, err)
			return
		}
		s.logf("uid %d: %s ok", uid, req.Op)
	case OpBackupRestore:
		rr := req.Restore
		if rr == nil {
			rr = &RestoreRequest{}
		}
		res, err := s.Ops.BackupRestore(cctx, rr, in)
		if err != nil {
			s.logf("uid %d: %s: error: %v", uid, req.Op, err)
			// Read the rest of the upload so the client can read our answer
			// instead of getting a reset.
			_, _ = io.Copy(io.Discard, io.LimitReader(in, 2<<30))
			reply(errorResponse{Error: err.Error()})
			return
		}
		s.logf("uid %d: %s ok (dryRun=%v, ok=%v)", uid, req.Op, res.DryRun, res.OK)
		reply(res)
	}
}
