package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"strings"
	"syscall"
	"time"
)

// DefaultSocket is the helper's socket path.
const DefaultSocket = "/run/aslcp/helper.sock"

// UnavailableError means the helper could not be reached.
type UnavailableError struct {
	Reason string
	Err    error
}

func (e *UnavailableError) Error() string { return e.Reason + ": " + e.Err.Error() }
func (e *UnavailableError) Unwrap() error { return e.Err }

// Client talks to the helper over its unix socket.
type Client struct {
	Socket  string
	Timeout time.Duration // per request; default 3 minutes
}

// NewClient returns a Client for socket.
func NewClient(socket string) *Client { return &Client{Socket: socket} }

// Do sends op and decodes the response into out (may be nil). A response
// carrying an "error" field is returned as an error.
func (c *Client) Do(ctx context.Context, op string, out any) error {
	return c.do(ctx, Request{Op: op}, out, 1<<20)
}

// maxConfigResponse bounds config-* responses (file lists, diffs).
const maxConfigResponse = 16 << 20

func (c *Client) do(ctx context.Context, req Request, out any, maxResp int64) error {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return &UnavailableError{Reason: unavailableReason(err), Err: err}
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	enc := json.NewEncoder(conn)
	enc.SetEscapeHTML(false) // keep config files from growing past the request cap
	if err := enc.Encode(req); err != nil {
		return fmt.Errorf("helper: %w", err)
	}
	// Nothing follows the request of a plain op: say so, so the server sees
	// EOF instead of waiting for more. (The stream ops of backup.go send an
	// archive after the request and never take this path.)
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	data, err := io.ReadAll(io.LimitReader(conn, maxResp))
	if err != nil {
		return fmt.Errorf("helper: %w", err)
	}
	var e errorResponse
	if json.Unmarshal(data, &e) == nil && e.Error != "" {
		if e.Error == ErrBusy.Error() {
			return ErrBusy
		}
		if e.Error == ErrAptBusy.Error() {
			return ErrAptBusy
		}
		for _, de := range dvErrors {
			if strings.HasPrefix(e.Error, de.Error()) {
				return fmt.Errorf("%w%s", de, strings.TrimPrefix(e.Error, de.Error()))
			}
		}
		if strings.HasPrefix(e.Error, conflictPrefix) {
			var files []string
			for _, f := range strings.Split(strings.TrimPrefix(e.Error, conflictPrefix), ", ") {
				if f != "" {
					files = append(files, f)
				}
			}
			return &ConflictError{Files: files}
		}
		return errors.New(e.Error)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("helper: bad response: %w", err)
		}
	}
	return nil
}

func unavailableReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "not installed from a package"
	case errors.Is(err, syscall.EACCES), errors.Is(err, fs.ErrPermission):
		return "no permission to use the helper"
	}
	return "helper not running"
}

// UpdateStatus calls update-status.
func (c *Client) UpdateStatus(ctx context.Context) (Status, error) {
	var s Status
	return s, c.Do(ctx, OpUpdateStatus, &s)
}

// UpdateCheck calls update-check.
func (c *Client) UpdateCheck(ctx context.Context) (Status, error) {
	var s Status
	return s, c.Do(ctx, OpUpdateCheck, &s)
}

// UpdateApply calls update-apply.
func (c *Client) UpdateApply(ctx context.Context) (Status, error) {
	var s Status
	return s, c.Do(ctx, OpUpdateApply, &s)
}

// SetChannel switches the update channel ("stable" or "beta").
func (c *Client) SetChannel(ctx context.Context, channel string) (Status, error) {
	var s Status
	switch channel {
	case ChannelStable:
		return s, c.Do(ctx, OpChannelStable, &s)
	case ChannelBeta:
		return s, c.Do(ctx, OpChannelBeta, &s)
	}
	return s, fmt.Errorf("invalid channel %q", channel)
}

// FirewallStatus calls firewall-status.
func (c *Client) FirewallStatus(ctx context.Context) (Firewall, error) {
	var f Firewall
	return f, c.Do(ctx, OpFirewallStatus, &f)
}

// FirewallOpen calls firewall-open.
func (c *Client) FirewallOpen(ctx context.Context) (Firewall, error) {
	var f Firewall
	return f, c.Do(ctx, OpFirewallOpen, &f)
}

func (c *Client) config(ctx context.Context, op string, cr *ConfigRequest, out any) error {
	return c.do(ctx, Request{Op: op, Config: cr}, out, maxConfigResponse)
}

// ConfigList calls config-list.
func (c *Client) ConfigList(ctx context.Context) ([]ConfigInfo, error) {
	var v []ConfigInfo
	return v, c.config(ctx, OpConfigList, nil, &v)
}

// ConfigRead calls config-read.
func (c *Client) ConfigRead(ctx context.Context, path string) (ConfigContent, error) {
	var v ConfigContent
	return v, c.config(ctx, OpConfigRead, &ConfigRequest{Path: path}, &v)
}

// ConfigApply calls config-apply. A conflict is returned as *ConflictError.
func (c *Client) ConfigApply(ctx context.Context, files []ConfigFile, message, author string) (ConfigApplyResult, error) {
	var v ConfigApplyResult
	return v, c.config(ctx, OpConfigApply, &ConfigRequest{Files: files, Message: message, Author: author}, &v)
}

// ConfigLog calls config-log.
func (c *Client) ConfigLog(ctx context.Context, limit, offset int) ([]ConfigCommit, error) {
	var v []ConfigCommit
	return v, c.config(ctx, OpConfigLog, &ConfigRequest{Limit: limit, Offset: offset}, &v)
}

// ConfigShow calls config-show.
func (c *Client) ConfigShow(ctx context.Context, commit string) (ConfigShowResult, error) {
	var v ConfigShowResult
	return v, c.config(ctx, OpConfigShow, &ConfigRequest{Commit: commit}, &v)
}

// ConfigRestore calls config-restore.
func (c *Client) ConfigRestore(ctx context.Context, commit, author string) (ConfigApplyResult, error) {
	var v ConfigApplyResult
	return v, c.config(ctx, OpConfigRestore, &ConfigRequest{Commit: commit, Author: author}, &v)
}

// ConfigRecord calls config-record: the helper commits the current content of
// a radio interface configuration (simpleusb.conf, usbradio.conf) to the
// history.
func (c *Client) ConfigRecord(ctx context.Context, path, message, author string) (ConfigApplyResult, error) {
	var v ConfigApplyResult
	return v, c.config(ctx, OpConfigRecord, &ConfigRequest{Path: path, Message: message, Author: author}, &v)
}

// AsteriskRestart calls asterisk-restart.
func (c *Client) AsteriskRestart(ctx context.Context) error {
	var v OKResult
	return c.config(ctx, OpAsteriskRestart, nil, &v)
}

// BackupInfo calls backup-info.
func (c *Client) BackupInfo(ctx context.Context) (BackupInfo, error) {
	var v BackupInfo
	return v, c.Do(ctx, OpBackupInfo, &v)
}

// streamTimeoutClient bounds a streaming request on the client side.
const streamTimeoutClient = 25 * time.Minute

func (c *Client) dialStream(ctx context.Context, req Request) (net.Conn, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, streamTimeoutClient)
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		cancel()
		return nil, nil, &UnavailableError{Reason: unavailableReason(err), Err: err}
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	closeAll := func() { stop(); cancel() }
	enc := json.NewEncoder(conn)
	if err := enc.Encode(req); err != nil {
		conn.Close()
		closeAll()
		return nil, nil, fmt.Errorf("helper: %w", err)
	}
	return conn, closeAll, nil
}

type streamCloser struct {
	conn net.Conn
	done context.CancelFunc
}

func (s streamCloser) Close() error {
	err := s.conn.Close()
	s.done()
	return err
}

// BackupCreate calls backup-create and returns the open stream (header and
// tar body). The caller must read it to the end and Close it; a body that
// ends early (the tar reader reports it) means the helper failed.
func (c *Client) BackupCreate(ctx context.Context, req BackupRequest) (*BackupStream, error) {
	conn, done, err := c.dialStream(ctx, Request{Op: OpBackupCreate, Backup: &req})
	if err != nil {
		return nil, err
	}
	sc := streamCloser{conn, done}
	bs, err := ReadBackupStream(conn, sc)
	if err != nil {
		sc.Close()
		return nil, err
	}
	return bs, nil
}

// BackupRestore calls backup-restore, sending archive (the gzip file) and
// returning the helper's result. With req.DryRun it only validates and
// returns the plan.
func (c *Client) BackupRestore(ctx context.Context, req RestoreRequest, archive io.Reader) (RestoreResult, error) {
	conn, done, err := c.dialStream(ctx, Request{Op: OpBackupRestore, Restore: &req})
	if err != nil {
		return RestoreResult{}, err
	}
	defer done()
	defer conn.Close()
	_, werr := io.Copy(conn, archive)
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	data, rerr := io.ReadAll(io.LimitReader(conn, maxConfigResponse))
	if len(data) == 0 {
		if werr != nil {
			return RestoreResult{}, fmt.Errorf("helper: %w", werr)
		}
		if rerr != nil {
			return RestoreResult{}, fmt.Errorf("helper: %w", rerr)
		}
		return RestoreResult{}, errors.New("helper: empty response")
	}
	var e errorResponse
	if json.Unmarshal(data, &e) == nil && e.Error != "" {
		if e.Error == ErrBusy.Error() {
			return RestoreResult{}, ErrBusy
		}
		return RestoreResult{}, errors.New(e.Error)
	}
	var res RestoreResult
	if err := json.Unmarshal(data, &res); err != nil {
		return RestoreResult{}, fmt.Errorf("helper: bad response: %w", err)
	}
	return res, nil
}

// OSCheck calls os-check: apt-get update and the list of upgradable packages.
// It can take minutes, so the request timeout is longer than usual.
func (c *Client) OSCheck(ctx context.Context) (OSStatus, error) {
	var s OSStatus
	cc := *c
	if cc.Timeout < osCallTimeout+30*time.Second {
		cc.Timeout = osCallTimeout + 30*time.Second
	}
	return s, cc.Do(ctx, OpOSCheck, &s)
}

// OSStatus calls os-status.
func (c *Client) OSStatus(ctx context.Context) (OSStatus, error) {
	var s OSStatus
	return s, c.Do(ctx, OpOSStatus, &s)
}

// OSApply calls os-apply.
func (c *Client) OSApply(ctx context.Context) (OSStatus, error) {
	var s OSStatus
	return s, c.Do(ctx, OpOSApply, &s)
}

// Reboot calls reboot.
func (c *Client) Reboot(ctx context.Context) error {
	var v OKResult
	return c.Do(ctx, OpReboot, &v)
}

// RestartASLCP calls aslcp-restart.
func (c *Client) RestartASLCP(ctx context.Context) error {
	var v OKResult
	return c.Do(ctx, OpAslcpRestart, &v)
}

// DVStatus calls dv-status.
func (c *Client) DVStatus(ctx context.Context) (DVStatus, error) {
	var s DVStatus
	return s, c.Do(ctx, OpDVStatus, &s)
}

// DVEnable calls dv-enable: the install job starts and the status comes back.
func (c *Client) DVEnable(ctx context.Context) (DVStatus, error) {
	var s DVStatus
	return s, c.Do(ctx, OpDVEnable, &s)
}

// DVDisable calls dv-disable.
func (c *Client) DVDisable(ctx context.Context) (DVStatus, error) {
	var s DVStatus
	return s, c.Do(ctx, OpDVDisable, &s)
}

// DVJob calls dv-job.
func (c *Client) DVJob(ctx context.Context) (DVJob, error) {
	var s DVJob
	return s, c.Do(ctx, OpDVJob, &s)
}

// DVConfig calls dv-config: the engine's config.json is rendered.
func (c *Client) DVConfig(ctx context.Context) (DVConfigResult, error) {
	var r DVConfigResult
	return r, c.Do(ctx, OpDVConfig, &r)
}

// DVNetworkGet calls dv-network without an argument: it reads.
func (c *Client) DVNetworkGet(ctx context.Context) (DVNetworkReply, error) {
	var r DVNetworkReply
	return r, c.Do(ctx, OpDVNetwork, &r)
}

// DVNetworkSet calls dv-network with an update of one network or the identity.
func (c *Client) DVNetworkSet(ctx context.Context, req DVNetworkRequest) (DVNetworkReply, error) {
	var r DVNetworkReply
	return r, c.do(ctx, Request{Op: OpDVNetwork, DV: &DVRequest{Network: &req}}, &r, 1<<20)
}

// DVRestart calls dv-restart.
func (c *Client) DVRestart(ctx context.Context) (DVStatus, error) {
	var s DVStatus
	return s, c.Do(ctx, OpDVRestart, &s)
}

// DVPrivateGet calls dv-private without a record: it reads.
func (c *Client) DVPrivateGet(ctx context.Context) (DVPrivateReply, error) {
	var r DVPrivateReply
	return r, c.Do(ctx, OpDVPrivate, &r)
}

// DVPrivateSet calls dv-private with a record.
func (c *Client) DVPrivateSet(ctx context.Context, rec DVPrivateRecord) (DVPrivateReply, error) {
	var r DVPrivateReply
	return r, c.do(ctx, Request{Op: OpDVPrivate, DV: &DVRequest{Private: &rec}}, &r, 1<<20)
}

// DVPrivateClear calls dv-private with clear.
func (c *Client) DVPrivateClear(ctx context.Context) (DVPrivateReply, error) {
	var r DVPrivateReply
	return r, c.do(ctx, Request{Op: OpDVPrivate, DV: &DVRequest{Clear: true}}, &r, 1<<20)
}
