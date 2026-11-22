// Package legacy names the previous third-party digital voice stack in one
// place. ASLCP refers to it only to install md380-emu from that stack's apt
// repository, and to stop, import and migrate old installs (state
// directories, settings keys, backup part ids, database tables). Nothing
// else in the code base spells these names.
package legacy

import "strings"

// External apt repository that ships md380-emu.
const (
	AptRepoURL   = "http://dvswitch.org/DVSwitch_Repository"
	KeyURL       = "https://dvswitch.org/DVSwitch_Repository/dvswitch-keyring.gpg"
	KeyringPath  = "/usr/share/keyrings/dvswitch-keyring.gpg"
	SourcesPath  = "/etc/apt/sources.list.d/dvswitch.list"
	AptListsGlob = "/var/lib/apt/lists/dvswitch.org*"

	// PackagePrefix is the vendor prefix of the old stack's package names.
	PackagePrefix = "dvswitch"
)

// IsLegacyPackage reports whether the package name belongs to the old stack's
// vendor namespace.
func IsLegacyPackage(name string) bool { return strings.HasPrefix(name, PackagePrefix) }

// Units are the systemd units (without ".service") of the old stack that
// enabling digital modes stops and disables.
var Units = []string{"analog_bridge", "mmdvm_bridge", "netcheck", "ysfgateway", "p25gateway", "nxdngateway", "stfu"}

// Names of ASLCP's own pre-2.0 data that 2.0 migrates or still accepts.
const (
	OldStateDir       = "/var/lib/aslcp-dvswitch"         // helper state directory
	OldHistoryDir     = "/var/lib/aslcp-dvswitch-history" // helper ini history
	OldSettingsPrefix = "dvswitch."                       // settings keys
	OldFavoritesTable = "dvswitch_favorites"              // database table
	OldBackupPart     = "dvswitch"                        // backup part id and archive directory
)

// Ini files of the old stack that backups carry (for the later import).
const (
	// IniMainID is the history id of the old stack's main ini and IniMainRel
	// its path below /opt.
	IniMainID  = "dvswitch"
	IniMainRel = "MMDVM_Bridge/DVSwitch.ini"
)
