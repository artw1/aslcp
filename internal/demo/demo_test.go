package demo

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestNodeList(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range NodeList() {
		if n.Node < "1997" || n.Node > "2002" || len(n.Node) != 4 {
			t.Errorf("node %q is not a placeholder", n.Node)
		}
		if seen[n.Node] {
			t.Errorf("node %q twice", n.Node)
		}
		seen[n.Node] = true
	}
	if !seen[LocalNode] {
		t.Fatalf("no local node %s", LocalNode)
	}
}

func TestRadioIDClient(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://radioid.example.net/user.csv", nil)
	resp, err := RadioIDClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || len(b) == 0 {
		t.Fatalf("%d %q", resp.StatusCode, b)
	}
}
