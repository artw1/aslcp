package schedules

import (
	"os"
	"strings"
	"time"
)

// ZoneSource abstracts the places the time zone name is looked up (tests
// inject fakes).
type ZoneSource struct {
	Getenv   func(string) string
	ReadFile func(string) ([]byte, error)
	Readlink func(string) (string, error)
}

// SystemZoneSource reads the real environment and file system.
func SystemZoneSource() ZoneSource {
	return ZoneSource{Getenv: os.Getenv, ReadFile: os.ReadFile, Readlink: os.Readlink}
}

// ZoneName returns the IANA name of the node's time zone: TZ, then
// /etc/timezone, then the /etc/localtime symlink target after "zoneinfo/";
// otherwise the abbreviation of loc at now (for example "UTC").
func ZoneName(src ZoneSource, loc *time.Location, now time.Time) string {
	valid := func(n string) bool {
		if n == "" || n == "Local" {
			return false
		}
		_, err := time.LoadLocation(n)
		return err == nil
	}
	if src.Getenv != nil {
		n := strings.TrimPrefix(strings.TrimSpace(src.Getenv("TZ")), ":")
		if valid(n) {
			return n
		}
	}
	if src.ReadFile != nil {
		if b, err := src.ReadFile("/etc/timezone"); err == nil {
			n := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
			if valid(n) {
				return n
			}
		}
	}
	if src.Readlink != nil {
		if t, err := src.Readlink("/etc/localtime"); err == nil {
			if i := strings.Index(t, "zoneinfo/"); i >= 0 {
				n := t[i+len("zoneinfo/"):]
				if valid(n) {
					return n
				}
			}
		}
	}
	if loc == nil {
		loc = time.Local
	}
	abbr, _ := now.In(loc).Zone()
	return abbr
}
