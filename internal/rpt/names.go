// Package rpt implements the AllStarLink app_rpt specifics on top of the
// generic AMI client: status parsing (RptStatus XStat/SawStat, RptLocalNodes),
// link control, typed events and a Monitor that keeps live node state.
//
// Formats were reconstructed from app_rpt's rpt_manager.c, rpt_link.c,
// rpt_functions.c and rpt_cli.c; see testdata/ for sample transcripts.
package rpt

import (
	"regexp"
	"strings"
)

// NodeKind classifies a link/node name.
type NodeKind int

const (
	KindUnknown     NodeKind = iota
	KindAllStar              // all digits: AllStar node number
	KindEchoLink             // 3 followed by six digits
	KindPhonePortal          // ends with "-P"
	KindClient               // starts with a letter: direct client (web transceiver, app, callsign)
)

func (k NodeKind) String() string {
	switch k {
	case KindAllStar:
		return "allstar"
	case KindEchoLink:
		return "echolink"
	case KindPhonePortal:
		return "phone"
	case KindClient:
		return "client"
	}
	return "unknown"
}

var (
	reDigits   = regexp.MustCompile(`^[0-9]+$`)
	reEchoLink = regexp.MustCompile(`^3[0-9]{6}$`)
)

// Classify classifies a node name as seen in the link table. EchoLink
// (3xxxxxx) is checked before generic digits because it is a subset of them.
func Classify(name string) NodeKind {
	switch {
	case name == "":
		return KindUnknown
	case reEchoLink.MatchString(name):
		return KindEchoLink
	case reDigits.MatchString(name):
		return KindAllStar
	case strings.HasSuffix(name, "-P"):
		return KindPhonePortal
	case (name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z'):
		return KindClient
	}
	return KindUnknown
}
