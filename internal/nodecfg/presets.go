package nodecfg

import "github.com/artw1/aslcp/internal/schema"

// Preset is a named bundle of node settings for the setup wizard. The values
// follow the duplex semantics documented in the stock rpt.conf.
type Preset struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Desc  string `json:"desc"`
	// Values maps schema key ids to values.
	Values map[string]string `json:"values"`
	// Hub: the preset also sets the radio channel to none (Local/pseudo).
	Hub bool `json:"hub,omitempty"`
}

var presets = []Preset{
	{Name: "simplex", Label: "Simplex node or hotspot",
		Desc: "One radio, half duplex, with courtesy tones and hang time. Audio is not repeated.",
		Values: map[string]string{
			"rpt.conf:node:duplex": "1", "rpt.conf:node:linktolink": "no"}},
	{Name: "halfduplexlink", Label: "Link radio, half duplex",
		Desc: "A link radio next to a repeater controller: half duplex, no tones and no hang time.",
		Values: map[string]string{
			"rpt.conf:node:duplex": "0", "rpt.conf:node:linktolink": "no"}},
	{Name: "repeater", Label: "Repeater",
		Desc: "Full duplex with tones and hang time; received audio is repeated.",
		Values: map[string]string{
			"rpt.conf:node:duplex": "2", "rpt.conf:node:linktolink": "no", "rpt.conf:node:hangtime": "2000"}},
	{Name: "hub", Label: "Hub, no radio",
		Desc: "A node without a radio that only links other nodes together.",
		Values: map[string]string{
			"rpt.conf:node:duplex": "0", "rpt.conf:node:linktolink": "no"},
		Hub: true},
}

// Presets returns the presets in a stable order.
func Presets() []Preset {
	out := make([]Preset, len(presets))
	copy(out, presets)
	return out
}

// PresetByName finds a preset.
func PresetByName(name string) (Preset, bool) {
	for _, p := range presets {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}

// SchemaDocument is the body of GET /api/config/schema.
type SchemaDocument struct {
	Keys    []schema.Key `json:"keys"`
	Forms   []string     `json:"forms"`
	Presets []Preset     `json:"presets"`
}

// SchemaDoc returns the schema document.
func SchemaDoc() SchemaDocument {
	return SchemaDocument{Keys: schema.All(), Forms: schema.Forms(), Presets: Presets()}
}
