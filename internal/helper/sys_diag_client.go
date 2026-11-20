package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// diagStream is the open answer of diag-collect: the gzip bytes.
type diagStream struct {
	r      *bufio.Reader
	closer io.Closer
}

func (d *diagStream) Read(p []byte) (int, error) { return d.r.Read(p) }
func (d *diagStream) Close() error               { return d.closer.Close() }

// DiagBundle calls diag-collect and returns the open stream of the .tar.gz.
// The caller reads it to the end and closes it. An error line from the helper
// becomes an error (ErrDiagBusy when another bundle is being built).
func (c *Client) DiagBundle(ctx context.Context, req DiagRequest) (io.ReadCloser, error) {
	conn, done, err := c.dialStream(ctx, Request{Op: OpDiagCollect, Diag: &req})
	if err != nil {
		return nil, err
	}
	sc := streamCloser{conn, done}
	br := bufio.NewReaderSize(conn, 64<<10)
	first, err := br.Peek(1)
	if err != nil {
		sc.Close()
		return nil, fmt.Errorf("helper: empty response")
	}
	if first[0] == '{' { // an error line; the gzip magic is 0x1f
		line, err := readLine(br, 64<<10)
		sc.Close()
		if err != nil {
			return nil, fmt.Errorf("helper: %w", err)
		}
		var e errorResponse
		if json.Unmarshal(line, &e) != nil || e.Error == "" {
			return nil, errors.New("helper: bad response")
		}
		if e.Error == ErrDiagBusy.Error() {
			return nil, ErrDiagBusy
		}
		return nil, errors.New(e.Error)
	}
	return &diagStream{r: br, closer: sc}, nil
}
