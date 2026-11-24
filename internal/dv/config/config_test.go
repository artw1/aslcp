package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/dv/ctl"
)

func TestDefaultsWhenMissing(t *testing.T) {
	c, found, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || found {
		t.Fatal(err, found)
	}
	if c.USRP.Listen != "127.0.0.1:34001" || c.USRP.Peer != "127.0.0.1:32001" ||
		c.Vocoder.MD380 != "127.0.0.1:2470" || c.Vocoder.TimeoutMS != 60 ||
		len(c.Networks) != 1 || c.Networks[0].Mode != ctl.ModeLoop {
		t.Fatalf("%+v", c)
	}
}

func TestLoadGood(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	doc := `{"version":1,"usrp":{"listen":"127.0.0.1:1234","peer":"127.0.0.1:1235","gainRxDb":3},
	 "identity":{"callsign":"N0CALL","dmrId":1234567},
	 "networks":[{"mode":"LOOP","enabled":true},{"mode":"BM","address":"192.0.2.1","port":62031,"password":"x"}],
	 "ctl":{"allowUids":[1000]}}`
	os.WriteFile(p, []byte(doc), 0o600)
	c, found, err := Load(p)
	if err != nil || !found {
		t.Fatal(err)
	}
	if c.Identity.DMRID != 1234567 || c.Vocoder.TimeoutMS != 60 || len(c.Networks) != 2 || c.USRP.GainRx != 3 {
		t.Fatalf("%+v", c)
	}
}

func TestStrict(t *testing.T) {
	bad := map[string]string{
		"unknown field": `{"version":1,"bogus":1}`,
		"version":       `{"version":2}`,
		"no version":    `{}`,
		"trailing":      `{"version":1} {"version":1}`,
		"bad addr":      `{"version":1,"usrp":{"listen":"nope"}}`,
		"port":          `{"version":1,"usrp":{"listen":"127.0.0.1:99999"}}`,
		"timeout":       `{"version":1,"vocoder":{"timeoutMs":5000}}`,
		"gain":          `{"version":1,"usrp":{"gainTxDb":50}}`,
		"mode":          `{"version":1,"networks":[{"mode":"XYZ"}]}`,
		"dup mode":      `{"version":1,"networks":[{"mode":"LOOP"},{"mode":"LOOP"}]}`,
		"not json":      `version`,
		"uid":           `{"version":1,"ctl":{"allowUids":[-1]}}`,
		"net port":      `{"version":1,"networks":[{"mode":"BM","port":70000}]}`,
	}
	for name, doc := range bad {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Parse([]byte(`{"version":1,"x":1}`)); err == nil || !strings.Contains(err.Error(), "config") {
		t.Errorf("err %v", err)
	}
}

// The echo test (LOOP) is part of every config and always on, whatever the
// file says.
func TestEchoTestAlwaysOn(t *testing.T) {
	for name, doc := range map[string]string{
		"absent":   `{"version":1,"networks":[{"mode":"BM","enabled":true}]}`,
		"disabled": `{"version":1,"networks":[{"mode":"BM"},{"mode":"LOOP","enabled":false}]}`,
		"no list":  `{"version":1}`,
	} {
		c, err := Parse([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		n := 0
		for _, nc := range c.Networks {
			if nc.Mode == ctl.ModeLoop {
				n++
				if !nc.Enabled {
					t.Errorf("%s: LOOP disabled", name)
				}
			}
		}
		if n != 1 {
			t.Errorf("%s: %d LOOP entries in %+v", name, n, c.Networks)
		}
	}
}

func TestM17Config(t *testing.T) {
	ok := `{"version":1,"identity":{"callsign":"N0CALL"},"networks":[{"mode":"M17","enabled":true,"address":"192.0.2.10","port":17000,"options":{"module":"C","can":"2","reflector":"M17-XXX","suffix":"B"}}]}`
	if _, err := Parse([]byte(ok)); err != nil {
		t.Fatal(err)
	}
	for name, doc := range map[string]string{
		"module":   `"options":{"module":"c"}`,
		"can":      `"options":{"can":"16"}`,
		"reflect":  `"options":{"reflector":"XXX"}`,
		"callsign": `"options":{"suffix":"TOOLONGSUF"}`,
	} {
		b := `{"version":1,"identity":{"callsign":"N0CALL"},"networks":[{"mode":"M17","enabled":true,` + doc + `}]}`
		if _, err := Parse([]byte(b)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// a disabled M17 needs no callsign
	if _, err := Parse([]byte(`{"version":1,"networks":[{"mode":"M17"}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]byte(`{"version":1,"networks":[{"mode":"M17","enabled":true}]}`)); err == nil {
		t.Fatal("enabled M17 without callsign accepted")
	}
}
