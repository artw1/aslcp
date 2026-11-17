// Package ami implements a small Asterisk Manager Interface (AMI) client
// using only the standard library.
//
// Wire format: a message is a sequence of "Key: Value" lines terminated by an
// empty line. Both CRLF and bare LF line endings are accepted. Keys are
// case-insensitive and may repeat (for example many "Output:" lines).
//
// Two non-standard shapes are handled:
//
//   - Lines that are not "Key: Value" (for example the raw XML returned by
//     app_rpt's RptLocalNodes) are kept, in order, in Message.Body.
//   - Legacy "Response: Follows" messages (Asterisk 1.x / hamvoip) carry free
//     text between the headers and a "--END COMMAND--" line; the text, which
//     may contain blank lines, ends up in Message.Body.
package ami

import "strings"

// Header is a single "Key: Value" pair.
type Header struct {
	Key   string
	Value string
}

// H is a shorthand constructor for Header.
func H(key, value string) Header { return Header{Key: key, Value: value} }

// Message is one AMI message (a response or an event).
//
// Messages delivered to subscribers are shared between them and must be
// treated as read-only.
type Message struct {
	// Fields holds the key/value pairs in wire order.
	Fields []Header
	// Body holds lines that were not key/value pairs, in wire order.
	Body []string
}

// Get returns the value of the first field named key (case-insensitive), or
// "" if there is none.
func (m *Message) Get(key string) string {
	for i := range m.Fields {
		if strings.EqualFold(m.Fields[i].Key, key) {
			return m.Fields[i].Value
		}
	}
	return ""
}

// Has reports whether a field named key exists.
func (m *Message) Has(key string) bool {
	for i := range m.Fields {
		if strings.EqualFold(m.Fields[i].Key, key) {
			return true
		}
	}
	return false
}

// All returns the values of every field named key, in wire order.
func (m *Message) All(key string) []string {
	var out []string
	for i := range m.Fields {
		if strings.EqualFold(m.Fields[i].Key, key) {
			out = append(out, m.Fields[i].Value)
		}
	}
	return out
}

// Lines returns the message as text lines: every field rendered as
// "Key: Value" (in wire order) followed by the Body lines.
func (m *Message) Lines() []string {
	out := make([]string, 0, len(m.Fields)+len(m.Body))
	for _, f := range m.Fields {
		out = append(out, f.Key+": "+f.Value)
	}
	return append(out, m.Body...)
}

// Response returns the value of the Response header ("" if absent).
func (m *Message) Response() string { return m.Get("Response") }

// Event returns the value of the Event header ("" if this is not an event).
func (m *Message) Event() string { return m.Get("Event") }

// splitField splits "Key: Value". Exactly one space after the colon is
// removed (Asterisk writes "Key: %s"), so leading indentation in values such
// as Command output is preserved. Lines whose key part is empty, contains
// whitespace or starts a markup tag are not fields.
func splitField(line string) (key, value string, ok bool) {
	i := strings.IndexByte(line, ':')
	if i <= 0 {
		return "", "", false
	}
	key = line[:i]
	if strings.ContainsAny(key, " \t<") {
		return "", "", false
	}
	value = strings.TrimPrefix(line[i+1:], " ")
	return key, value, true
}
