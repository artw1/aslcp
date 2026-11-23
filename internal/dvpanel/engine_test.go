package dvpanel

import (
	"errors"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
)

func TestInfoFromStatus(t *testing.T) {
	st := ctl.Status{Started: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Mode: "LOOP", Target: &ctl.Target{TG: 9990, Private: true}}
	in := InfoFromStatus(st)
	if !in.Found || in.Mode != "LOOP" || in.Tune != "9990" || in.Started != "2026-01-02T03:04:05Z" {
		t.Fatalf("%+v", in)
	}
	in = InfoFromStatus(ctl.Status{Mode: "BM", Target: &ctl.Target{TG: 91, Slot: 2}})
	if in.Tune != "91" || in.Slot != "2" || in.Private {
		t.Fatalf("%+v", in)
	}
	in = InfoFromStatus(ctl.Status{Mode: "TGIF", Target: &ctl.Target{TG: 1234567, Private: true}})
	if in.Tune != "1234567#" || !in.Private || in.TG != "1234567" {
		t.Fatalf("%+v", in)
	}
	in = InfoFromStatus(ctl.Status{Mode: "YSF", Target: &ctl.Target{Room: "00001", Host: "192.0.2.20", Port: 42000}})
	if in.Tune != "00001" {
		t.Fatalf("%+v", in)
	}
	in = InfoFromStatus(ctl.Status{Mode: "YSF", Target: &ctl.Target{Host: "192.0.2.20", Port: 42000}})
	if in.Tune != "192.0.2.20:42000" {
		t.Fatalf("%+v", in)
	}
	if in = InfoFromStatus(ctl.Status{}); !in.Found || in.Mode != "" || in.Tune != "" {
		t.Fatalf("%+v", in)
	}
	if in = InfoFromStatus(ctl.Status{Mode: "NOPE", Target: &ctl.Target{TG: 1}}); in.Mode != "" || in.Tune != "" {
		t.Fatalf("%+v", in)
	}
}

func TestBuildTune(t *testing.T) {
	res := func(m, v string) (ctl.Target, bool) {
		if v == "00001" {
			return ctl.Target{Room: v, Host: "192.0.2.20", Port: 42000}, true
		}
		return ctl.Target{}, false
	}
	a, un, err := BuildTune("LOOP", "9990", res)
	if err != nil || un || a.Mode != "LOOP" || a.Target.TG != 9990 || !a.Target.Private {
		t.Fatalf("%+v %v %v", a, un, err)
	}
	if _, un, err = BuildTune("LOOP", "disconnect", res); err != nil || !un {
		t.Fatal(un, err)
	}
	a, un, err = BuildTune("BM", "1234567#", res)
	if err != nil || un || a.Target.TG != 1234567 || !a.Target.Private {
		t.Fatalf("%+v %v", a, err)
	}
	if _, un, err = BuildTune("DMR", "4000", res); err != nil || !un {
		t.Fatal(un, err)
	}
	if _, un, err = BuildTune("P25", "9999", res); err != nil || !un {
		t.Fatal(un, err)
	}
	a, _, err = BuildTune("YSF", "00001", res)
	if err != nil || a.Target.Host != "192.0.2.20" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, _, err = BuildTune("YSF", "00002", res); !errors.Is(err, ErrUnknownTarget) {
		t.Fatal(err)
	}
	res17 := func(mode, v string) (ctl.Target, bool) {
		if mode == "M17" && v == "M17-XXX C" {
			return ctl.Target{Room: v, Name: "M17-XXX", Host: "192.0.2.30", Port: 17000}, true
		}
		return ctl.Target{}, false
	}
	a, _, err = BuildTune("M17", "M17-XXX C", res17)
	if err != nil || a.Mode != "M17" || a.Target.Room != "M17-XXX C" || a.Target.Port != 17000 {
		t.Fatalf("%+v %v", a, err)
	}
	if _, un, err = BuildTune("M17", "disconnect", res17); err != nil || !un {
		t.Fatal(un, err)
	}
	if _, _, err = BuildTune("M17", "M17-ZZZ C", res17); !errors.Is(err, ErrUnknownTarget) {
		t.Fatal(err)
	}
	if _, _, err = BuildTune("M17", "M17-XXX", res17); !errors.Is(err, ErrBadTune) {
		t.Fatal(err)
	}
	if in := InfoFromStatus(ctl.Status{Mode: "M17", Target: &ctl.Target{Room: "M17-XXX C", Host: "192.0.2.30", Port: 17000}}); in.Mode != "M17" || in.Tune != "M17-XXX C" || in.TG != "M17-XXX C" {
		t.Fatalf("%+v", in)
	}
	if _, _, err = BuildTune("P25", "10100", nil); !errors.Is(err, ErrUnknownTarget) {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"LOOP", "91"}, {"XX", "1"}, {"BM", ""}, {"BM", "a"}} {
		if _, _, err = BuildTune(c[0], c[1], res); !errors.Is(err, ErrBadTune) {
			t.Errorf("%v: %v", c, err)
		}
	}
}
