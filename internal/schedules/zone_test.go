package schedules

import (
	"errors"
	"testing"
	"time"
)

func fakeSrc(env map[string]string, files map[string]string, links map[string]string) ZoneSource {
	return ZoneSource{
		Getenv: func(k string) string { return env[k] },
		ReadFile: func(p string) ([]byte, error) {
			if v, ok := files[p]; ok {
				return []byte(v), nil
			}
			return nil, errors.New("nope")
		},
		Readlink: func(p string) (string, error) {
			if v, ok := links[p]; ok {
				return v, nil
			}
			return "", errors.New("nope")
		},
	}
}

func TestZoneName(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		src  ZoneSource
		loc  *time.Location
		want string
	}{
		{"TZ", fakeSrc(map[string]string{"TZ": "Europe/Berlin"}, map[string]string{"/etc/timezone": "America/Chicago\n"}, nil), time.UTC, "Europe/Berlin"},
		{"TZ with colon", fakeSrc(map[string]string{"TZ": ":Europe/Berlin"}, nil, nil), time.UTC, "Europe/Berlin"},
		{"timezone file", fakeSrc(nil, map[string]string{"/etc/timezone": "America/Chicago\n"}, map[string]string{"/etc/localtime": "/usr/share/zoneinfo/Asia/Tokyo"}), time.UTC, "America/Chicago"},
		{"symlink", fakeSrc(nil, nil, map[string]string{"/etc/localtime": "/usr/share/zoneinfo/Asia/Tokyo"}), time.UTC, "Asia/Tokyo"},
		{"relative symlink", fakeSrc(nil, nil, map[string]string{"/etc/localtime": "../usr/share/zoneinfo/Etc/UTC"}), time.UTC, "Etc/UTC"},
		{"invalid TZ falls through", fakeSrc(map[string]string{"TZ": "Not/AZone"}, nil, map[string]string{"/etc/localtime": "/usr/share/zoneinfo/Asia/Tokyo"}), time.UTC, "Asia/Tokyo"},
		{"abbreviation", fakeSrc(nil, nil, nil), mustLoc("America/New_York"), "EST"},
		{"abbreviation UTC", fakeSrc(nil, nil, nil), time.UTC, "UTC"},
		{"nil funcs", ZoneSource{}, time.UTC, "UTC"},
	}
	for _, c := range cases {
		if got := ZoneName(c.src, c.loc, now); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
