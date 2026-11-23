package schema

import (
	"regexp"
	"strings"
	"testing"
)

func TestKeysAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range All() {
		if seen[k.ID] {
			t.Errorf("duplicate id %s", k.ID)
		}
		seen[k.ID] = true
		if k.ID != k.File+":"+k.Scope+":"+k.Name {
			t.Errorf("%s: id does not match file/scope/name", k.ID)
		}
		if k.Label == "" || k.Desc == "" || k.Form == "" {
			t.Errorf("%s: missing label, description or form", k.ID)
		}
		if k.Reload == ReloadVerified {
			t.Errorf("%s: no key is verified on hardware yet", k.ID)
		}
		if k.Reload != ReloadAssumed && k.Reload != ReloadRestart {
			t.Errorf("%s: reload class %q", k.ID, k.Reload)
		}
		if k.Type == TypeEnum && (len(k.Values) == 0 || len(k.Values) != len(k.Labels)) {
			t.Errorf("%s: enum values and labels differ", k.ID)
		}
		if k.Default != "" {
			if err := k.Validate(k.Default); err != nil {
				t.Errorf("%s: default %q is invalid: %v", k.ID, k.Default, err)
			}
		}
		// no real-looking data in descriptions
		if regexp.MustCompile(`@[a-z0-9-]+\.[a-z]{2,}`).MatchString(k.Desc) {
			t.Errorf("%s: description has an address", k.ID)
		}
	}
	if k, ok := Lookup("rpt.conf:node:duplex"); !ok || k.Reload != ReloadRestart {
		t.Errorf("duplex = %+v", k)
	}
	if _, ok := Find("rpt.conf", ScopeNode, "hangtime"); !ok {
		t.Error("Find hangtime")
	}
	for _, k := range ForForm(FormEchoLink) {
		if k.Name == "pwd" && !k.Secret {
			t.Error("echolink pwd is not secret")
		}
	}
	if k, _ := Lookup("manager.conf:admin:secret"); !k.Secret {
		t.Error("manager secret is not secret")
	}
	if len(Forms()) < 6 {
		t.Errorf("forms = %v", Forms())
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		id, v string
		ok    bool
	}{
		{"rpt.conf:node:duplex", "2", true},
		{"rpt.conf:node:duplex", "5", false},
		{"rpt.conf:node:duplex", "x", false},
		{"rpt.conf:node:hangtime", "2000", true},
		{"rpt.conf:node:hangtime", "-1", false},
		{"rpt.conf:node:hangtime", "1.5", false},
		{"rpt.conf:node:hangtime", "", false},
		{"rpt.conf:node:telemdynamic", "yes", true},
		{"rpt.conf:node:telemdynamic", "maybe", false},
		{"rpt.conf:node:holdofftelem", "1", true},
		{"rpt.conf:node:holdofftelem", "yes", false},
		{"rpt.conf:node:statpost_url", "", true}, // optional
		{"rpt.conf:node:statpost_url", "https://stats.example.net/post", true},
		{"rpt.conf:node:statpost_url", DefaultStatpostURL, true},
		{"rpt.conf:node:statpost_url", "ftp://stats.example.net/x", false},
		{"rpt.conf:node:statpost_url", "stats.example.net", false},
		{"rpt.conf:node:statpost_url", "http://", false},
		{"rpt.conf:node:statpost_url", "http://stats.example.net/a b", false},
		{"rpt.conf:node:statpost_url", "http://stats.example.net/$(x)", false},
		{"rpt.conf:node:statpost_url", "a ; b", false},
		{"rpt.conf:node:statpost_url", " lead", false},
		{"rpt.conf:node:statpost_url", "two\nlines", false},
		{"echolink.conf:el0:astnode", "1999", true},
		{"echolink.conf:el0:astnode", "abc", false},
	}
	for _, c := range cases {
		k, ok := Lookup(c.id)
		if !ok {
			t.Fatalf("no key %s", c.id)
		}
		err := k.Validate(c.v)
		if (err == nil) != c.ok {
			t.Errorf("%s = %q: err = %v, want ok=%v", c.id, c.v, err, c.ok)
		}
		if err != nil {
			if ve, isVE := err.(*ValidateError); !isVE || ve.Msg == "" || strings.Contains(ve.Msg, "%!") {
				t.Errorf("%s: bad error %v", c.id, err)
			}
		}
	}
}
