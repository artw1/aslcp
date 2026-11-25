package dvpanel

// Info is what the resume tracker and the snapshot read from the engine: its
// current mode and target in the shape the panel shows. Found is false while
// the engine is not connected.
type Info struct {
	Found bool `json:"found"`
	// Started is the engine's start time (RFC 3339, UTC), Updated the time of
	// its last state change.
	Started string `json:"started,omitempty"`
	Updated string `json:"updated,omitempty"`
	// Mode is a panel mode ("" when none is set). Tune is the target as text:
	// the talkgroup for the DMR networks, the reflector or TG for YSF, P25 and
	// NXDN, "" when not tuned.
	Mode string `json:"mode"`
	Tune string `json:"tune"`
	TG   string `json:"tg"`
	Slot string `json:"slot"`
	// Private: the target is a DMR private call (TG is then the number that
	// is called, not a talkgroup).
	Private bool `json:"private,omitempty"`
}

// Caller is one call as the panel shows it. The fields and their JSON names
// are the ones of helper.DVCaller.
type Caller struct {
	Call    string `json:"call,omitempty"`
	Private bool   `json:"private,omitempty"`
	DMRID   string `json:"dmrId,omitempty"`
	Dst     string `json:"dst,omitempty"`
	// Mode is the engine mode of a YSF call only ("YSF"; the other modes leave
	// it empty), DstName the reflector's name when it is known. For YSF Dst is
	// the reflector (an id or an address), not a talkgroup.
	Mode    string `json:"mode,omitempty"`
	DstName string `json:"dstName,omitempty"`
	Slot    int    `json:"slot,omitempty"`
	At      string `json:"at,omitempty"`
}
