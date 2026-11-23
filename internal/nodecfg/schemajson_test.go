package nodecfg

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite web/mock/schema.json")

const mockSchema = "../../web/mock/schema.json"

// TestSchemaJSON keeps the dev mock's copy of the schema in step with the Go
// one. Run `go test ./internal/nodecfg -run TestSchemaJSON -update` after
// changing internal/schema or the presets.
func TestSchemaJSON(t *testing.T) {
	want, err := json.MarshalIndent(SchemaDoc(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if *update {
		if err := os.WriteFile(mockSchema, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(mockSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("web/mock/schema.json is out of date: run go test ./internal/nodecfg -run TestSchemaJSON -update")
	}
}
