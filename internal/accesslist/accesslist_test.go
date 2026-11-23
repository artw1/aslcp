package accesslist

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseShow(t *testing.T) {
	lines := []string{
		"/allowlist/2001/1999                              : 1",
		"/allowlist/2001/N0CALL                            : web phone",
		"/denylist/2001/2002                               : my note",
		"/denylist/2001/2003/extra                         : 1",
		"/denylist/2001/0123                               : 1",
		"/denylist/abc/2002                                : 1",
		"/other/2001/2002                                  : 1",
		"/allowlist/2001/bad;x                             : 1",
		"3 results found.",
	}
	got := ParseShow(lines)
	want := []Entry{
		{Family: Allow, Local: "2001", Remote: "1999"},
		{Family: Allow, Local: "2001", Remote: "N0CALL", Web: true, Note: "web phone"},
		{Family: Deny, Local: "2001", Remote: "2002", Note: "my note"},
		// "0123" has a leading zero: a callsign-shaped key, read only
		{Family: Deny, Local: "2001", Remote: "0123", Web: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestModeOf(t *testing.T) {
	a, d := []Entry{{Remote: "1"}}, []Entry{{Remote: "2"}}
	for _, c := range []struct {
		a, d []Entry
		want string
	}{{nil, nil, "open"}, {nil, d, "deny"}, {a, nil, "allow"}, {a, d, "allow"}} {
		if got := ModeOf(c.a, c.d); got != c.want {
			t.Errorf("ModeOf(%d,%d) = %s want %s", len(c.a), len(c.d), got, c.want)
		}
	}
}

func TestValidators(t *testing.T) {
	for s, want := range map[string]bool{"1": true, "1999": true, "2001": true, "1234567": true, "": false, "0": false, "01": false, "12345678": false, "12a": false, "1/2": false, "1 2": false} {
		if ValidNode(s) != want {
			t.Errorf("ValidNode(%q) = %v", s, !want)
		}
	}
	if ValidNote("a;b") || ValidNote("a\nb") || ValidNote(strings.Repeat("x", 41)) || !ValidNote("") || !ValidNote("my hub-1.x_y") {
		t.Error("ValidNote")
	}
}

func TestEnforcedCorpus(t *testing.T) {
	if Enforced("[default]\nexten => i,1,Hangup\n") {
		t.Error("a dialplan without the families is not enforcing")
	}
	if !Enforced("GotoIf($[${DB_KEYCOUNT(allowlist/${EXTEN})} = 0]?denylist)\nDB_EXISTS(denylist/x)") {
		t.Error("the stock dialplan enforces the lists")
	}
}
