// Package sa818 programs the SA818 radio module (SHARI and similar boards)
// over its USB serial bridge (a CH340, USB id 1a86:7523, 9600 8N1) with the
// module's AT commands. The module cannot report its settings back, so every
// successful programming run is saved to a JSON file the caller names.
package sa818
