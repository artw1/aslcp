package ami

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

const (
	maxLineLen  = 1 << 20
	maxMsgLines = 200000
)

var errLineTooLong = errors.New("ami: line too long")
var errMsgTooLong = errors.New("ami: message too long")

// readLine reads one line without its trailing "\n" or "\r\n".
func readLine(r *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		chunk, err := r.ReadSlice('\n')
		if sb.Len()+len(chunk) > maxLineLen {
			return "", errLineTooLong
		}
		sb.Write(chunk)
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err == io.EOF && sb.Len() > 0 {
			break
		}
		return "", err
	}
	s := sb.String()
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	return s, nil
}

// readMessage reads one message. Leading blank lines are skipped.
func readMessage(r *bufio.Reader) (*Message, error) {
	m := &Message{}
	follows := false // inside a legacy "Response: Follows" message
	ended := false   // saw --END COMMAND--
	n := 0
	for {
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		if n++; n > maxMsgLines {
			return nil, errMsgTooLong
		}
		if line == "" {
			if follows && !ended {
				// A blank line inside free-form command text.
				m.Body = append(m.Body, "")
				continue
			}
			if len(m.Fields) == 0 && len(m.Body) == 0 {
				continue // skip stray blank lines between messages
			}
			return m, nil
		}
		if follows && !ended {
			if line == "--END COMMAND--" {
				ended = true
				continue
			}
			// Headers precede the text in a Follows response.
			if k, v, ok := splitField(line); ok && len(m.Body) == 0 &&
				(strings.EqualFold(k, "Privilege") || strings.EqualFold(k, "ActionID")) {
				m.Fields = append(m.Fields, Header{k, v})
			} else {
				m.Body = append(m.Body, line)
			}
			continue
		}
		if k, v, ok := splitField(line); ok {
			m.Fields = append(m.Fields, Header{k, v})
			if strings.EqualFold(k, "Response") && strings.EqualFold(strings.TrimSpace(v), "Follows") {
				follows = true
			}
		} else {
			m.Body = append(m.Body, line)
		}
	}
}

// ReadMessage reads the next message from r using the same rules as a live
// connection (blank-line terminated; bare LF accepted; XML/raw body lines
// kept in Body; legacy "Follows" text). It returns io.EOF at end of input.
// Useful for loading captured transcripts in tests.
func ReadMessage(r *bufio.Reader) (*Message, error) { return readMessage(r) }
