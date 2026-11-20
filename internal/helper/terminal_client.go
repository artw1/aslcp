package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// terminalConn is the client end of a terminal stream: reads go through the
// buffer that read the JSON reply, so no frame byte is lost.
type terminalConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *terminalConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// OpenTerminal calls terminal.open and returns the framed stream (see
// terminal_frame.go). The caller closes it; the helper then hangs the session
// up. rows and cols of zero mean the default size.
func (c *Client) OpenTerminal(ctx context.Context, rows, cols uint16) (io.ReadWriteCloser, error) {
	var d net.Dialer
	hctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := d.DialContext(hctx, "unix", c.Socket)
	if err != nil {
		return nil, &UnavailableError{Reason: unavailableReason(err), Err: err}
	}
	ok := false
	defer func() {
		if !ok {
			conn.Close()
		}
	}()
	if dl, has := hctx.Deadline(); has {
		_ = conn.SetDeadline(dl)
	}
	if err := json.NewEncoder(conn).Encode(Request{Op: OpTerminalOpen, Terminal: &TerminalRequest{Rows: rows, Cols: cols}}); err != nil {
		return nil, fmt.Errorf("helper: %w", err)
	}
	br := bufio.NewReaderSize(conn, 4096)
	line, err := br.ReadSlice('\n')
	if err != nil {
		return nil, fmt.Errorf("helper: %w", err)
	}
	var e errorResponse
	if json.Unmarshal(line, &e) == nil && e.Error != "" {
		return nil, errors.New(e.Error)
	}
	var res OKResult
	if err := json.Unmarshal(line, &res); err != nil || !res.OK {
		return nil, errors.New("helper: bad response")
	}
	_ = conn.SetDeadline(time.Time{})
	ok = true
	return &terminalConn{Conn: conn, r: br}, nil
}
