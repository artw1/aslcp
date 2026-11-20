package helper

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/artw1/aslcp/internal/backup"
)

// Backup operations. backup-info is a plain request/response operation.
// backup-create and backup-restore are streaming operations: the request is
// still one small JSON object (at most 4 KiB, like the plain operations).
//
//   - backup-create answers with one JSON line, the BackupHeader, followed by
//     a plain tar stream with the system part of the backup (entries named
//     as in the header, content verified against the header's sha256) and
//     EOF. A failure before the header is an {"error": ...} line; a failure
//     later cuts the stream short, which the tar reader reports.
//   - backup-restore is followed by the gzip-compressed archive (the file the
//     user downloaded) on the same connection; the client half-closes its
//     side after the last byte. The answer is one JSON RestoreResult.
const (
	OpBackupInfo    = "backup-info"
	OpBackupCreate  = "backup-create"
	OpBackupRestore = "backup-restore"
)

// BackupRequest is the argument of backup-create: the optional parts.
type BackupRequest struct {
	History bool `json:"history"`
	Allmon3 bool `json:"allmon3"`
	Sounds  bool `json:"sounds"`
	// DVSwitch includes the DVSwitch ini files and dvsm.macro, secrets
	// included (docs/BACKUP.md).
	DVSwitch bool `json:"dvswitch"`
}

// BackupPartInfo describes one optional part on this node.
type BackupPartInfo struct {
	ID      string `json:"id"` // history | allmon3 | sounds | dvswitch
	Present bool   `json:"present"`
	Size    int64  `json:"size"`
}

// BackupInfo is the result of backup-info.
type BackupInfo struct {
	Parts []BackupPartInfo `json:"parts"`
	SA818 bool             `json:"sa818"`
}

// BackupHeader is the first line of a backup-create stream.
type BackupHeader struct {
	Arch            string                `json:"arch"`
	ASL3Version     string                `json:"asl3Version"`
	AsteriskVersion string                `json:"asteriskVersion"`
	OSID            string                `json:"osId,omitempty"`
	OSCodename      string                `json:"osCodename,omitempty"`
	OSName          string                `json:"osName,omitempty"`
	Channel         string                `json:"channel"`
	Nodes           []string              `json:"nodes"`
	Packages        []backup.Package      `json:"packages"`
	Parts           backup.Parts          `json:"parts"`
	MissingStock    []backup.MissingStock `json:"missingStock"`
	Skipped         []string              `json:"skipped"`
	Entries         []backup.Entry        `json:"entries"`
}

// RestoreRequest is the argument of backup-restore.
type RestoreRequest struct {
	// DryRun validates the archive and returns the plan without changing
	// anything.
	DryRun bool `json:"dryRun"`
	// DeleteMissing lists stock files (relative to /etc/asterisk) the user
	// ticked "also delete here" for. Each must be an offered deletion.
	DeleteMissing []string `json:"deleteMissing"`
}

// Restore plan actions.
const (
	ActRestore        = "restore"
	ActRestoreDist    = "restore_dist"
	ActKeepStock      = "keep_stock"
	ActKeepNew        = "keep_new"
	ActDeleteOptional = "delete_optional"
	ActSkip           = "skip"
)

// RestoreFile is one /etc/asterisk file of the plan.
type RestoreFile struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
}

// RestoreStep is one step of an apply.
type RestoreStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// RestoreResult is the result of backup-restore.
type RestoreResult struct {
	DryRun bool   `json:"dryRun"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	// Files is the plan for /etc/asterisk (also filled after an apply).
	Files []RestoreFile `json:"files"`
	Steps []RestoreStep `json:"steps"`
	// The state of this node, for the version warnings and the list of
	// missing packages.
	ASL3Version      string           `json:"asl3Version"`
	AsteriskVersion  string           `json:"asteriskVersion"`
	Arch             string           `json:"arch"`
	OSID             string           `json:"osId,omitempty"`
	OSCodename       string           `json:"osCodename,omitempty"`
	OSName           string           `json:"osName,omitempty"`
	Packages         []backup.Package `json:"packages"`
	SA818Port        string           `json:"sa818Port"`
	SA818PortPresent bool             `json:"sa818PortPresent"`
	// DVSwitchNotInstalled is set when the backup holds the DVSwitch part
	// but this node has no /opt/Analog_Bridge: the part is not restored.
	DVSwitchNotInstalled bool `json:"dvswitchNotInstalled,omitempty"`
}

// isStreamOp reports whether op carries a byte stream.
func isStreamOp(op string) bool {
	return op == OpBackupCreate || op == OpBackupRestore || op == OpDiagCollect
}

// maxBackupHeader bounds the header line a client accepts.
const maxBackupHeader = 64 << 20

// BackupStream is an open backup-create answer: the header and the tar
// stream behind it. Close it when done.
type BackupStream struct {
	Header BackupHeader
	Body   io.Reader
	closer io.Closer
}

// Close releases the connection.
func (b *BackupStream) Close() error {
	if b.closer == nil {
		return nil
	}
	return b.closer.Close()
}

// ReadBackupStream parses the header line of a backup-create answer from r.
func ReadBackupStream(r io.Reader, closer io.Closer) (*BackupStream, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	line, err := readLine(br, maxBackupHeader)
	if err != nil {
		return nil, fmt.Errorf("helper: %w", err)
	}
	var e errorResponse
	if json.Unmarshal(line, &e) == nil && e.Error != "" {
		return nil, errors.New(e.Error)
	}
	var h BackupHeader
	if err := json.Unmarshal(line, &h); err != nil {
		return nil, fmt.Errorf("helper: bad response: %w", err)
	}
	return &BackupStream{Header: h, Body: br, closer: closer}, nil
}

func readLine(br *bufio.Reader, max int) ([]byte, error) {
	var out []byte
	for {
		chunk, err := br.ReadSlice('\n')
		out = append(out, chunk...)
		if len(out) > max {
			return nil, errors.New("header too large")
		}
		if err == nil {
			return out, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			if len(out) > 0 && errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, err
		}
	}
}
