// Package helper implements the wire protocol, the server logic and the
// client of aslcp-helper, the small root-owned process that performs the only
// privileged operations ASLCP needs (package updates, firewalld).
//
// Protocol: one JSON request per connection, one JSON response. Operations
// are a fixed set; requests carry no arguments (except the strictly typed config field of the config-* ops), so nothing a client sends can
// reach a shell or alter a command line.
package helper

import "strings"

// Operation names.
const (
	OpConfigList    = "config-list"
	OpConfigRead    = "config-read"
	OpConfigApply   = "config-apply"
	OpConfigLog     = "config-log"
	OpConfigShow    = "config-show"
	OpConfigRestore = "config-restore"
	// OpConfigRecord commits the current on-disk content of simpleusb.conf
	// or usbradio.conf to the history as an external change (the file was
	// rewritten by Asterisk itself, for example by "susb tune save").
	OpConfigRecord    = "config-record"
	OpAsteriskRestart = "asterisk-restart"

	OpUpdateStatus   = "update-status"
	OpUpdateCheck    = "update-check"
	OpUpdateApply    = "update-apply"
	OpFirewallStatus = "firewall-status"
	OpFirewallOpen   = "firewall-open"
	// OpChannelStable and OpChannelBeta switch the apt suite of the aslcp
	// source between "stable" and "beta" (the update channel).
	OpChannelStable = "update-channel-stable"
	OpChannelBeta   = "update-channel-beta"

	// Operating system (apt) updates. None takes an argument: the commands
	// are fixed, no package name ever comes from a client.
	OpOSCheck  = "os-check"  // apt-get update + simulated upgrade
	OpOSStatus = "os-status" // job state and log, reboot-required (cheap)
	OpOSApply  = "os-apply"  // start the upgrade in a transient unit
	OpReboot   = "reboot"    // systemctl reboot
	// OpAslcpRestart restarts the aslcp service (systemctl --no-block).
	OpAslcpRestart = "aslcp-restart"
)

// Update channels (the suite of the aslcp apt source).
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// Job states reported in Status.Job.
const (
	JobIdle      = "idle"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	// JobNoop: the job finished but apt had nothing to upgrade (the package
	// index lags behind latest.json).
	JobNoop = "noop"
)

// Request is the single request object.
type Request struct {
	Op string `json:"op"`
	// Config is accepted by the config-* operations only; every other
	// operation rejects a request that carries it.
	Config *ConfigRequest `json:"config,omitempty"`
	// Backup is accepted by backup-create only, Restore by backup-restore
	// only (see backup_proto.go).
	Backup  *BackupRequest  `json:"backup,omitempty"`
	Restore *RestoreRequest `json:"restore,omitempty"`
}

// Status is the response of the update-* operations. Candidate and
// Upgradable are kept for protocol compatibility but are no longer filled in:
// the server compares versions itself (latest.json), apt is not consulted.
type Status struct {
	Installed  string `json:"installed"`
	Candidate  string `json:"candidate"`
	Upgradable bool   `json:"upgradable"`
	Job        string `json:"job"`
	JobLog     string `json:"jobLog"`
	// Channel is the suite of the aslcp apt source: "stable", "beta", or ""
	// when it cannot be determined.
	Channel string `json:"channel"`
}

// OSPackage is one package an apt upgrade would install.
type OSPackage struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	To       string `json:"to"`
	Origin   string `json:"origin,omitempty"`
	Security bool   `json:"security"`
}

// OSStatus is the response of os-check, os-status and os-apply. Packages and
// KeptBack are filled by os-check only.
type OSStatus struct {
	Packages       []OSPackage `json:"packages"`
	KeptBack       []string    `json:"keptBack"`
	RebootRequired bool        `json:"rebootRequired"`
	RebootPkgs     []string    `json:"rebootPkgs"`
	Job            string      `json:"job"`
	JobLog         string      `json:"jobLog"`
}

// Firewall is the response of the firewall-* operations.
type Firewall struct {
	// Present: firewall-cmd is installed.
	Present bool `json:"present"`
	// Running: firewalld is running.
	Running bool `json:"running"`
	// ServiceEnabled: the "aslcp" firewalld service is enabled in the
	// runtime configuration.
	ServiceEnabled bool `json:"serviceEnabled"`
}

// errorResponse is sent for any failure.
type errorResponse struct {
	Error string `json:"error"`
}

// ConfigRequest is the argument of the config-* operations.
type ConfigRequest struct {
	Path    string       `json:"path,omitempty"`    // config-read
	Files   []ConfigFile `json:"files,omitempty"`   // config-apply
	Message string       `json:"message,omitempty"` // commit message (<= 200 chars, one line)
	Author  string       `json:"author,omitempty"`  // ASLCP username (<= 64, [A-Za-z0-9._-])
	Commit  string       `json:"commit,omitempty"`  // config-show / config-restore: 7-40 hex
	Limit   int          `json:"limit,omitempty"`   // config-log (1-200)
	Offset  int          `json:"offset,omitempty"`
}

// ConfigFile is one file of a config-apply.
type ConfigFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Base    string `json:"base"` // sha256 hex of the version edited; "" = new file
}

// ConfigInfo is one entry of config-list.
type ConfigInfo struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"` // unix seconds
	SHA256 string `json:"sha256"`
	Mode   string `json:"mode"` // octal, e.g. "0644"
	Owner  string `json:"owner"`
	Group  string `json:"group"`
}

// ConfigContent is the result of config-read.
type ConfigContent struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

// ConfigApplyResult is the result of config-apply and config-restore.
type ConfigApplyResult struct {
	Commit string `json:"commit"`
	Before string `json:"before"`
	// Files are the paths actually written (additive, used for the reload plan).
	Files []string `json:"files"`
	// Skipped (restore only): files left alone.
	Skipped []string `json:"skipped,omitempty"`
}

// ConfigCommit is one entry of config-log.
type ConfigCommit struct {
	Commit  string   `json:"commit"`
	Date    string   `json:"date"`
	Author  string   `json:"author"`
	Message string   `json:"message"`
	Files   []string `json:"files"`
}

// ConfigFileDiff is one file of config-show.
type ConfigFileDiff struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added | modified | deleted
	Diff   string `json:"diff"`
}

// ConfigShowResult is the result of config-show.
type ConfigShowResult struct {
	Commit  string           `json:"commit"`
	Date    string           `json:"date"`
	Author  string           `json:"author"`
	Message string           `json:"message"`
	Files   []ConfigFileDiff `json:"files"`
}

// OKResult is the result of asterisk-restart.
type OKResult struct {
	OK bool `json:"ok"`
}

// ConflictError is returned by config-apply when files changed on disk.
type ConflictError struct{ Files []string }

func (e *ConflictError) Error() string { return conflictPrefix + strings.Join(e.Files, ", ") }

const conflictPrefix = "conflict: "
