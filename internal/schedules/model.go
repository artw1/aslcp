// Package schedules runs scheduled connects and disconnects of links (see
// docs/SCHEDULES.md). It depends on small interfaces only: the store and the
// link controller are injected.
package schedules

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Actions and modes.
const (
	ActionConnect    = "connect"
	ActionDisconnect = "disconnect"

	ModeTransceive   = "transceive"
	ModeMonitor      = "monitor"
	ModeLocalMonitor = "localmonitor"
)

// MaxName is the longest schedule name.
const MaxName = 60

// Schedule is one scheduled connect or disconnect (the API's JSON shape).
type Schedule struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	Node       string     `json:"node"`
	Target     string     `json:"target"`
	Action     string     `json:"action"`
	Mode       string     `json:"mode"`
	Permanent  bool       `json:"permanent"`
	Exclusive  bool       `json:"exclusive"`
	Days       []int      `json:"days"`
	Date       string     `json:"date"`
	Start      string     `json:"start"`
	End        string     `json:"end"`
	LastRun    *time.Time `json:"lastRun"`
	LastResult string     `json:"lastResult"`
}

var (
	reTarget = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
	reClock  = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
)

// ErrInvalid wraps every validation error.
var ErrInvalid = errors.New("invalid schedule")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Normalize validates the user-editable fields and puts them in canonical
// form (trimmed, sorted days, defaults, fields that do not apply cleared).
// It does not check that Node is a local node. The returned error wraps
// ErrInvalid; its text is meant for the user.
func (s *Schedule) Normalize() error {
	s.Name = strings.TrimSpace(s.Name)
	if utf8.RuneCountInString(s.Name) > MaxName {
		return invalid("name is longer than %d characters", MaxName)
	}
	for _, r := range s.Name {
		if r < 0x20 || r == 0x7f {
			return invalid("name has control characters")
		}
	}
	s.Node = strings.TrimSpace(s.Node)
	s.Target = strings.TrimSpace(s.Target)
	if !reTarget.MatchString(s.Target) {
		return invalid("target must be a node number")
	}
	if s.Target == s.Node {
		return invalid("target must not be the local node")
	}
	switch s.Action {
	case ActionConnect:
		switch s.Mode {
		case "":
			s.Mode = ModeTransceive
		case ModeTransceive, ModeMonitor, ModeLocalMonitor:
		default:
			return invalid(`mode must be "transceive", "monitor" or "localmonitor"`)
		}
		if s.Mode == ModeLocalMonitor {
			s.Exclusive = false
		}
	case ActionDisconnect:
		s.Mode = ""
		s.Exclusive = false
		if s.End != "" {
			return invalid("a disconnect has no end time")
		}
	default:
		return invalid(`action must be "connect" or "disconnect"`)
	}
	if !reClock.MatchString(s.Start) {
		return invalid("start must be HH:MM (24 h)")
	}
	if s.End != "" {
		if !reClock.MatchString(s.End) {
			return invalid("end must be HH:MM (24 h)")
		}
		if s.End == s.Start {
			return invalid("end must differ from start")
		}
	}
	if len(s.Days) > 0 && s.Date != "" {
		return invalid("set either days or a date, not both")
	}
	if len(s.Days) == 0 && s.Date == "" {
		return invalid("set the days of the week or a date")
	}
	if s.Date != "" {
		if _, err := time.Parse("2006-01-02", s.Date); err != nil {
			return invalid("date must be YYYY-MM-DD")
		}
		s.Days = []int{}
	} else {
		seen := map[int]bool{}
		days := make([]int, 0, len(s.Days))
		for _, d := range s.Days {
			if d < 0 || d > 6 {
				return invalid("days must be 0 (Sunday) to 6 (Saturday)")
			}
			if !seen[d] {
				seen[d] = true
				days = append(days, d)
			}
		}
		sort.Ints(days)
		s.Days = days
	}
	return nil
}

// isWindow reports a connect with an end time.
func (s Schedule) isWindow() bool { return s.Action == ActionConnect && s.End != "" }

func clockMinutes(hhmm string) int {
	if len(hhmm) != 5 {
		return 0
	}
	return int(hhmm[0]-'0')*600 + int(hhmm[1]-'0')*60 + int(hhmm[3]-'0')*10 + int(hhmm[4]-'0')
}
