package sa818

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Clock is the time source for reply timeouts and retry pauses.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

// Reply is a parsed module answer such as "+DMOSETGROUP:0".
type Reply struct {
	// Name is the text between '+' and ':' ("DMOSETGROUP").
	Name string
	// Code is the value after the colon.
	Code string
}

// OK reports whether the module accepted the command (code 0).
func (r Reply) OK() bool { return r.Code == "0" }

// ErrNoReply means the module did not answer in time.
var ErrNoReply = errors.New("sa818: no reply from module")

// ParseReply parses one reply line.
func ParseReply(line string) (Reply, error) {
	line = strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(line, "+")
	if !ok {
		return Reply{}, fmt.Errorf("sa818: unexpected reply %q", line)
	}
	name, code, ok := strings.Cut(rest, ":")
	if !ok || name == "" {
		return Reply{}, fmt.Errorf("sa818: malformed reply %q", line)
	}
	return Reply{Name: name, Code: strings.TrimSpace(code)}, nil
}

// Client talks AT commands over a serial connection.
type Client struct {
	rw io.ReadWriter
	// Timeout is how long to wait for one reply. Default 2 s.
	Timeout time.Duration
	// Retries is how many times AT+DMOCONNECT is tried. Default 3.
	Retries int
	Clock   Clock
}

// NewClient returns a client on rw. Over a real port, rw must return from Read
// within a short time even when idle (the port from [OpenSerial] does, with a
// read timeout of 100 ms).
func NewClient(rw io.ReadWriter) *Client {
	return &Client{rw: rw, Timeout: 2 * time.Second, Retries: 3, Clock: realClock{}}
}

// Exec sends one command and returns the parsed reply.
func (c *Client) Exec(cmd string) (Reply, error) {
	if _, err := io.WriteString(c.rw, cmd+"\r\n"); err != nil {
		return Reply{}, fmt.Errorf("sa818: write %s: %w", cmdName(cmd), err)
	}
	line, err := c.readLine()
	if err != nil {
		return Reply{}, fmt.Errorf("sa818: %s: %w", cmdName(cmd), err)
	}
	return ParseReply(line)
}

func cmdName(cmd string) string {
	name, _, _ := strings.Cut(cmd, "=")
	return name
}

// readLine reads up to the first line feed, skipping blank lines.
func (c *Client) readLine() (string, error) {
	deadline := c.Clock.Now().Add(c.Timeout)
	var buf []byte
	tmp := make([]byte, 64)
	for {
		n, err := c.rw.Read(tmp)
		buf = append(buf, tmp[:n]...)
		for {
			i := strings.IndexByte(string(buf), '\n')
			if i < 0 {
				break
			}
			line := strings.TrimSpace(string(buf[:i]))
			buf = buf[i+1:]
			if line != "" {
				return line, nil
			}
		}
		if err != nil && !errors.Is(err, errTimeout) {
			if errors.Is(err, io.EOF) {
				return "", ErrNoReply
			}
			return "", err
		}
		if !c.Clock.Now().Before(deadline) {
			return "", ErrNoReply
		}
		if n == 0 {
			c.Clock.Sleep(10 * time.Millisecond)
		}
	}
}

// errTimeout is what a serial read returns when nothing arrived in its read
// timeout; the client keeps waiting until its own deadline.
var errTimeout = errors.New("sa818: read timeout")

// Connect checks that the module answers (AT+DMOCONNECT), retrying a few
// times because it can be slow right after power up.
func (c *Client) Connect() error {
	var last error
	for i := 0; i < max(c.Retries, 1); i++ {
		r, err := c.Exec("AT+DMOCONNECT")
		if err == nil && r.Name == "DMOCONNECT" && r.OK() {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("sa818: connect answered %+v", r)
		}
		last = err
		c.Clock.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("sa818: module did not answer connect: %w", last)
}

// Apply programs the module with s: connect, group, volume, filters. It stops
// at the first failure. It does not touch the saved state file; use
// [Programmer] for that.
func (c *Client) Apply(s Settings) error {
	cmds, err := s.Commands()
	if err != nil {
		return err
	}
	if err := c.Connect(); err != nil {
		return err
	}
	for _, cmd := range cmds {
		r, err := c.Exec(cmd)
		if err != nil {
			return err
		}
		if !r.OK() {
			return fmt.Errorf("sa818: %s refused (code %s)", cmdName(cmd), strconv.Quote(r.Code))
		}
	}
	return nil
}
