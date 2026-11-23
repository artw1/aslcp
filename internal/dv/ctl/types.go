// Package ctl is the control contract between the aslcp server and aslcp-dv:
// newline-delimited JSON over the unix socket /run/aslcp-dv/ctl.sock.
package ctl

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	SocketPath    = "/run/aslcp-dv/ctl.sock"
	ConfigPath    = "/var/lib/aslcp-dv/config.json"
	ConfigVersion = 1
	MaxLine       = 64 << 10
)

// Ops.
const (
	OpPing      = "ping"
	OpStatus    = "status"
	OpReload    = "reload"    // re-read ConfigPath (the "apply config" request)
	OpMode      = "mode"      // ModeArgs
	OpTune      = "tune"      // TuneArgs
	OpUnlink    = "unlink"    // UnlinkArgs
	OpSubscribe = "subscribe" // connection becomes an event stream
)

// Error codes.
const (
	CodeBadRequest   = "bad_request"
	CodeForbidden    = "forbidden"
	CodeBadConfig    = "bad_config"
	CodeNotAvailable = "not_available" // network not implemented in this build
	CodeNotEnabled   = "not_enabled"   // network disabled in config
	CodeBusy         = "busy"          // a call is in progress and the op would cut it
	CodeInternal     = "internal"
)

type Request struct {
	ID   uint64          `json:"id"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

type Response struct {
	ID     uint64          `json:"id"`
	OK     bool            `json:"ok"`
	Code   string          `json:"code,omitempty"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"` // Status for status/subscribe/mode/tune/unlink/reload
}

// Mode ids. M1 implements ModeLoop only.
const (
	ModeNone = ""
	ModeLoop = "LOOP"
	ModeBM   = "BM"
	ModeTGIF = "TGIF"
	ModeDMR  = "DMR"
	ModeYSF  = "YSF"
	ModeP25  = "P25"
	ModeNXDN = "NXDN"
)

// TGIFAddress and TGIFPort are the built-in TGIF master, used when the DMR
// host list is missing or has no TGIF entry.
const (
	TGIFAddress = "tgif.network"
	TGIFPort    = 62031
)

// DefaultHomebrewPassword is the login password of a DMR - Other master when
// none is set: the value most Homebrew masters ship with. BrandMeister and
// TGIF have no such default.
const DefaultHomebrewPassword = "passw0rd"

// EchoTestCall is the source callsign of the echo test's playback call (mode
// LOOP, shown as "Echo test" in the panel). It is not a valid callsign (a space),
// so it never collides with a station.
const EchoTestCall = "ECHO TEST"

// EchoTestName is how the panel names that caller and the mode (Last heard,
// Now calling).
const EchoTestName = "Echo test"

// IsEchoTest reports whether t is the echo test's playback call.
func IsEchoTest(t Talk) bool { return t.Mode == ModeLoop && t.Dir == "rx" && t.Call == EchoTestCall }

type ModeArgs struct {
	Mode  string `json:"mode"`
	Force bool   `json:"force,omitempty"` // cut an active call
}

// Target is where a tune points. For DMR-family modes TG (and Private) are
// used; for YSF/P25/NXDN the server resolves the host list entry first.
type Target struct {
	TG      uint32 `json:"tg,omitempty"`
	Slot    int    `json:"slot,omitempty"` // 1 or 2; 0 = network default
	Private bool   `json:"private,omitempty"`
	Room    string `json:"room,omitempty"` // YSF reflector id / P25,NXDN TG as text
	Name    string `json:"name,omitempty"`
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
}

type TuneArgs struct {
	Mode   string `json:"mode"`
	Target Target `json:"target"`
	Force  bool   `json:"force,omitempty"`
}

type UnlinkArgs struct {
	Mode string `json:"mode,omitempty"` // "" = current
}

type LoginState string

const (
	LoginOff     LoginState = "off"
	LoginPending LoginState = "pending"
	LoginOK      LoginState = "ok"
	LoginFailed  LoginState = "failed"
)

// DetailNoReply starts the Detail of a pending or failed network login whose
// reflector (YSF, P25, NXDN) has sent nothing for the link timeout: no poll
// reply, no voice. The dashboard shows it as "Reflector not answering".
const DetailNoReply = "no reply from "

// NotAnswering reports whether a network's login and detail say that its
// reflector does not answer (the engine's lost state, DetailNoReply).
func NotAnswering(l LoginState, detail string) bool {
	return (l == LoginPending || l == LoginFailed) && strings.HasPrefix(detail, DetailNoReply)
}

type NetStatus struct {
	Mode      string     `json:"mode"`
	Available bool       `json:"available"` // implemented in this build
	Enabled   bool       `json:"enabled"`   // enabled in config
	Login     LoginState `json:"login"`
	Detail    string     `json:"detail,omitempty"`
}

type VocoderStatus struct {
	OK       bool   `json:"ok"`
	Addr     string `json:"addr"`
	Frames   uint64 `json:"frames"`
	Timeouts uint64 `json:"timeouts"`
}

type Status struct {
	Version  string        `json:"version"`
	Started  time.Time     `json:"started"`
	Mode     string        `json:"mode"`
	Target   *Target       `json:"target,omitempty"`
	Updated  time.Time     `json:"updated"`
	USRP     USRPStatus    `json:"usrp"`
	Vocoder  VocoderStatus `json:"vocoder"`
	Networks []NetStatus   `json:"networks"`
	Talk     *Talk         `json:"talk,omitempty"` // call in progress
}

type USRPStatus struct {
	Listen   string    `json:"listen"` // 127.0.0.1:34001
	Peer     string    `json:"peer"`   // 127.0.0.1:32001
	LastRx   time.Time `json:"lastRx,omitempty"`
	RxFrames uint64    `json:"rxFrames"`
	TxFrames uint64    `json:"txFrames"`
}

// Talk is one call. Dir rx = from the network to the node, tx = from the node.
type Talk struct {
	ID       uint64        `json:"id"`
	Dir      string        `json:"dir"` // "rx" | "tx"
	Mode     string        `json:"mode"`
	Call     string        `json:"call,omitempty"`
	DMRID    uint32        `json:"dmrId,omitempty"`
	TG       uint32        `json:"tg,omitempty"`
	Room     string        `json:"room,omitempty"`
	Slot     int           `json:"slot,omitempty"`
	Private  bool          `json:"private,omitempty"`
	Start    time.Time     `json:"start"`
	End      time.Time     `json:"end,omitempty"`
	Duration time.Duration `json:"durationNs,omitempty"`
}

// Event types.
const (
	EvStatus    = "status"     // Status changed (mode/target/network)
	EvTalkStart = "talk_start" // Talk
	EvTalkEnd   = "talk_end"   // Talk with End and Duration
	EvLogin     = "login"      // Login
	EvVocoder   = "vocoder"    // VocoderStatus
	EvPing      = "ping"
	EvOverflow  = "overflow" // subscriber too slow; server closes
)

type Event struct {
	Type    string         `json:"type"`
	At      time.Time      `json:"at"`
	Status  *Status        `json:"status,omitempty"`
	Talk    *Talk          `json:"talk,omitempty"`
	Login   *LoginEvent    `json:"login,omitempty"`
	Vocoder *VocoderStatus `json:"vocoder,omitempty"`
}

type LoginEvent struct {
	Mode   string     `json:"mode"`
	State  LoginState `json:"state"`
	Detail string     `json:"detail,omitempty"`
}

// Config is ConfigPath, rendered by aslcp-helper (dv-config), mode 0600 aslcp-dv.
type Config struct {
	Version  int           `json:"version"`
	USRP     USRPConfig    `json:"usrp"`
	Vocoder  VocoderConfig `json:"vocoder"`
	Identity Identity      `json:"identity"`
	Networks []NetConfig   `json:"networks"`
	Ctl      CtlConfig     `json:"ctl"`
	Resume   *TuneArgs     `json:"resume,omitempty"` // tune at start (written by the server via helper in M2; ignored if nil)
}

type USRPConfig struct {
	Listen string `json:"listen"`             // "127.0.0.1:34001"
	Peer   string `json:"peer"`               // "127.0.0.1:32001"
	GainRx int    `json:"gainRxDb,omitempty"` // -20..20
	GainTx int    `json:"gainTxDb,omitempty"`
}

type VocoderConfig struct {
	MD380     string `json:"md380"`     // "127.0.0.1:2470"
	TimeoutMS int    `json:"timeoutMs"` // default 60
}

type Identity struct {
	Callsign   string `json:"callsign,omitempty"` // tests: N0CALL
	DMRID      uint32 `json:"dmrId,omitempty"`    // tests: 1234567
	RepeaterID uint32 `json:"repeaterId,omitempty"`
}

// NetConfig carries secrets: never logged, never in Status.
type NetConfig struct {
	Mode     string            `json:"mode"`
	Enabled  bool              `json:"enabled"`
	Address  string            `json:"address,omitempty"`
	Port     int               `json:"port,omitempty"`
	Password string            `json:"password,omitempty"`
	Options  map[string]string `json:"options,omitempty"`
}

type CtlConfig struct {
	Socket    string `json:"socket,omitempty"`
	AllowUIDs []int  `json:"allowUids,omitempty"` // tests only; default root + user aslcp
}
