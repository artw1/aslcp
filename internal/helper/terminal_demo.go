package helper

import (
	"context"
	"errors"
	"io"
	"net"
)

// The terminal part of the API's SystemHost for --demo: a pretend login
// prompt that echoes what is typed and never lets anyone in. No process is
// started and nothing on the machine is touched. There is nothing to detach
// from, so the session list is always empty.

func (d *DemoSystem) OpenTerminal(_ context.Context, rows, cols uint16, _ int64) (io.ReadWriteCloser, string, error) {
	client, server := net.Pipe()
	go demoLogin(server)
	return client, "0000000000000000", nil
}

func (d *DemoSystem) AttachTerminal(context.Context, string, int64, uint16, uint16) (io.ReadWriteCloser, error) {
	return nil, errors.New("no such session")
}

func (d *DemoSystem) ListTerminals(context.Context, []string) (TerminalList, error) {
	return TerminalList{Sessions: []TerminalInfo{}}, nil
}

func (d *DemoSystem) EndTerminal(context.Context, string) error { return nil }

func demoLogin(c net.Conn) {
	defer c.Close()
	out := func(s string) bool { return WriteData(c, []byte(s)) == nil }
	if !out("Demo mode: this is not a real login.\r\n\r\nnode2001 login: ") {
		return
	}
	for {
		typ, p, err := ReadFrame(c)
		if err != nil {
			return
		}
		if typ == FrameEnd {
			return
		}
		if typ != FrameData {
			continue
		}
		for _, b := range p {
			switch b {
			case '\r', '\n':
				if !out("\r\nPassword: \r\nLogin incorrect (demo)\r\n\r\nnode2001 login: ") {
					return
				}
			case 0x7f, 0x08:
				if !out("\b \b") {
					return
				}
			default:
				if b >= 0x20 && b < 0x7f && !out(string(rune(b))) {
					return
				}
			}
		}
	}
}
