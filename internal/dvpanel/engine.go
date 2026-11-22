package dvpanel

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
)

// ErrBadTune: the tune value is not valid for the mode.
var ErrBadTune = errors.New("the tune value is not valid for this mode")

// ErrUnknownTarget: a YSF, P25 or NXDN value that no host list knows.
var ErrUnknownTarget = errors.New("the target is not in the host list")

// InfoFromStatus turns the engine's status into what the panel and the
// resume tracker read. A status that was read is Found; the caller leaves
// Found false when the engine does not answer.
//
// Tune for LOOP is the parrot ID while a target is set, for the DMR networks
// the talkgroup, for YSF the reflector id (or host:port when the tune had no
// id), for P25 and NXDN the talkgroup.
func InfoFromStatus(st ctl.Status) Info {
	in := Info{Found: true, Mode: NormMode(st.Mode)}
	if !st.Started.IsZero() {
		in.Started = st.Started.UTC().Format(time.RFC3339)
	}
	if !st.Updated.IsZero() {
		in.Updated = st.Updated.UTC().Format(time.RFC3339)
	}
	t := st.Target
	if t == nil || in.Mode == "" {
		return in
	}
	in.Slot = strconv.Itoa(t.Slot)
	if t.Slot == 0 {
		in.Slot = ""
	}
	switch {
	case in.Mode == ModeLoop:
		in.Tune, in.TG = ParrotID, ParrotID
	case DMRFamily(in.Mode):
		if t.TG != 0 {
			in.TG = strconv.FormatUint(uint64(t.TG), 10)
		}
		in.Private = t.Private
		in.Tune = in.TG
		if t.Private && in.Tune != "" {
			in.Tune += PrivateMark
		}
	default:
		switch {
		case t.Room != "":
			in.Tune = t.Room
		case t.Host != "" && t.Port != 0:
			in.Tune = t.Host + ":" + strconv.Itoa(t.Port)
		}
		in.TG = in.Tune
	}
	return in
}

// BuildTune turns a panel tune into the engine call. unlink is true when the
// value is the mode's unlink value: the caller then sends the engine's unlink
// op instead (args holds only the mode). resolve maps a YSF, P25 or NXDN
// value to the host list entry; ErrUnknownTarget when it has none.
func BuildTune(mode, tune string, resolve func(mode, value string) (ctl.Target, bool)) (args ctl.TuneArgs, unlink bool, err error) {
	mode = NormMode(mode)
	if mode == "" || !ValidTune(mode, tune) {
		return ctl.TuneArgs{}, false, ErrBadTune
	}
	args.Mode = mode
	if UnlinkValue(mode, tune) {
		return args, true, nil
	}
	switch {
	case mode == ModeLoop:
		args.Target = ctl.Target{TG: 9990, Private: true}
	case DMRFamily(mode):
		n, perr := strconv.ParseUint(strings.TrimSuffix(tune, PrivateMark), 10, 32)
		if perr != nil {
			return ctl.TuneArgs{}, false, ErrBadTune
		}
		args.Target = ctl.Target{TG: uint32(n), Private: strings.HasSuffix(tune, PrivateMark)}
	default:
		if resolve == nil {
			return ctl.TuneArgs{}, false, ErrUnknownTarget
		}
		t, ok := resolve(mode, tune)
		if !ok {
			return ctl.TuneArgs{}, false, ErrUnknownTarget
		}
		args.Target = t
	}
	return args, false, nil
}
