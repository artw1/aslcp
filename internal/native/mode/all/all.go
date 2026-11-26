// Package all registers every node mode. Import it for its side effect.
package all

import (
	// Each mode registers itself with internal/native/mode.
	_ "github.com/artw1/aslcp/internal/native/mode/allstar"
	_ "github.com/artw1/aslcp/internal/native/mode/dv"
	_ "github.com/artw1/aslcp/internal/native/mode/echolink"
)
