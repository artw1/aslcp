package schedules

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	good := Schedule{Node: "1997", Target: " 41234 ", Action: "connect", Days: []int{4, 2, 4}, Start: "19:00", End: "20:30", Name: "  Net "}
	if err := good.Normalize(); err != nil {
		t.Fatal(err)
	}
	if good.Target != "41234" || good.Name != "Net" || good.Mode != ModeTransceive || len(good.Days) != 2 || good.Days[0] != 2 || good.Days[1] != 4 {
		t.Fatalf("%+v", good)
	}
	once := Schedule{Node: "1997", Target: "41234", Action: "disconnect", Mode: "monitor", Exclusive: true, Date: "2026-12-01", Start: "07:00"}
	if err := once.Normalize(); err != nil {
		t.Fatal(err)
	}
	if once.Mode != "" || once.Exclusive || once.Days == nil || len(once.Days) != 0 {
		t.Fatalf("%+v", once)
	}
	lm := Schedule{Node: "1997", Target: "41234", Action: "connect", Mode: "localmonitor", Exclusive: true, Days: []int{1}, Start: "07:00"}
	if err := lm.Normalize(); err != nil || lm.Exclusive {
		t.Fatalf("%+v %v", lm, err)
	}
	cases := map[string]func(s *Schedule){
		"target must be":          func(s *Schedule) { s.Target = "N0AAA" },
		"target must not":         func(s *Schedule) { s.Target = "1997" },
		"action":                  func(s *Schedule) { s.Action = "toggle" },
		"mode":                    func(s *Schedule) { s.Mode = "listen" },
		"no end":                  func(s *Schedule) { s.Action, s.End = "disconnect", "10:00" },
		"start must":              func(s *Schedule) { s.Start = "7:00" },
		"start must be ":          func(s *Schedule) { s.Start = "24:00" },
		"end must be":             func(s *Schedule) { s.End = "20:61" },
		"end must differ":         func(s *Schedule) { s.End = s.Start },
		"not both":                func(s *Schedule) { s.Date = "2026-12-01" },
		"set the days":            func(s *Schedule) { s.Days = nil },
		"date must":               func(s *Schedule) { s.Days, s.Date = nil, "2026-13-01" },
		"days must":               func(s *Schedule) { s.Days = []int{7} },
		"name is longer":          func(s *Schedule) { s.Name = strings.Repeat("x", 61) },
		"name has control":        func(s *Schedule) { s.Name = "a\nb" },
		"date must be YYYY-MM-DD": func(s *Schedule) { s.Days, s.Date = nil, "12/01/2026" },
	}
	for want, mut := range cases {
		s := Schedule{Node: "1997", Target: "41234", Action: "connect", Days: []int{2}, Start: "19:00", End: "20:30"}
		mut(&s)
		err := s.Normalize()
		if err == nil || !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: no error (%v)", want, err)
			continue
		}
		if !strings.Contains(err.Error(), strings.TrimSpace(want)) && want != "no end" && want != "set the days" {
			t.Errorf("%q: error %q", want, err)
		}
	}
}
