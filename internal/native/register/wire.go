package register

import (
	"bytes"
	"encoding/json"
)

// successMarker is the substring the registrar puts in its reply data when a
// registration is accepted.
const successMarker = "successfully registered"

type nodeEntry struct {
	Node   string `json:"node"`
	Passwd string `json:"passwd"`
	Remote int    `json:"remote"` // always 0
}

type regBody struct {
	Port int `json:"port,omitempty"`
	Data struct {
		Nodes map[string]nodeEntry `json:"nodes"`
	} `json:"data"`
}

type regReply struct {
	IPAddr  string          `json:"ipaddr"`
	Port    int             `json:"port"`
	Refresh int             `json:"refresh"`
	Data    json.RawMessage `json:"data"`
}

func buildBody(node, password string, port int) ([]byte, error) {
	var b regBody
	b.Port = port
	b.Data.Nodes = map[string]nodeEntry{node: {Node: node, Passwd: password}}
	return json.Marshal(b)
}

func (r regReply) ok() bool {
	return bytes.Contains(r.Data, []byte(successMarker))
}
