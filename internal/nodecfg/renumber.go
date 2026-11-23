package nodecfg

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/artw1/aslcp/internal/astconf"
)

// renumber changes every place the node number appears in the files the
// planner owns (docs/DESIGN.md 4.3): the node stanza name and its [nodes]
// entry in rpt.conf, the device section (dev, when the section is named after
// the node), extensions.conf [globals] NODE and echolink.conf astnode.
// rxchannel and the registration line are written by planRequest. It runs
// last: the other edits locate the stanza by its old name.
func (ed *editor) renumber(old, node string, si *stanzaInfo, dev string) error {
	sec, err := ed.section(si.sec)
	if err != nil {
		return err
	}
	sec.Rename(node)
	ed.touched[si.sec.File] = true
	ed.out = append(ed.out, Applied{Key: "[node]", File: si.sec.File, Section: node, Action: "set", From: "[" + old + "]", To: "[" + node + "]", Note: "node stanza renamed"})

	if err := ed.renumberNodesEntry(old, node); err != nil {
		return err
	}
	if dev != "" {
		f, err := ed.file(FileSimple)
		if err != nil {
			return err
		}
		ss := f.Section(old)
		if len(ss) != 1 {
			return refuse(Refusal{Code: "multiple_stanzas", Message: fmt.Sprintf("[%s] is not defined exactly once in %s.", old, FileSimple), File: FileSimple})
		}
		if len(f.Section(node)) > 0 {
			return invalid("%s already has a [%s] section", FileSimple, node)
		}
		ss[0].Rename(node)
		ed.touched[FileSimple] = true
		ed.out = append(ed.out, Applied{Key: "[device]", File: FileSimple, Section: node, Action: "set", From: "[" + old + "]", To: "[" + node + "]", Note: "device section renamed"})
	}
	if ed.w.Has(FileExt) {
		f, err := ed.file(FileExt)
		if err != nil {
			return err
		}
		for _, s := range f.Section("globals") {
			for _, e := range s.Get("NODE") {
				if e.Value != old {
					continue
				}
				e.SetValue(node)
				ed.touched[FileExt] = true
				ed.out = append(ed.out, Applied{Key: "NODE", File: FileExt, Section: "globals", Action: "set", From: old, To: node})
			}
		}
	}
	if ed.w.Has(FileEcho) {
		f, err := ed.file(FileEcho)
		if err != nil {
			return err
		}
		for _, s := range f.Section("el0") {
			for _, e := range s.Get("astnode") {
				if e.Value != old {
					continue
				}
				e.SetValue(node)
				ed.touched[FileEcho] = true
				ed.out = append(ed.out, Applied{Key: "astnode", File: FileEcho, Section: "el0", Action: "set", From: old, To: node})
			}
		}
	}
	return nil
}

// renumberNodesEntry renames the node's line of [nodes] ("1999 =
// radio@127.0.0.1/1999,NONE") and the number inside its value. A node with no
// line there is left alone.
func (ed *editor) renumberNodesEntry(old, node string) error {
	f, err := ed.file(FileRpt)
	if err != nil {
		return err
	}
	var es []*astconf.Entry
	for _, s := range f.Section("nodes") {
		es = append(es, s.Get(old)...)
	}
	switch {
	case len(es) == 0:
		ed.out = append(ed.out, Applied{Key: old, File: FileRpt, Section: "nodes", Action: "unchanged", Note: "the node has no line in [nodes]"})
		return nil
	case len(es) > 1:
		return refuse(Refusal{Code: "repeated_key", Message: fmt.Sprintf("[nodes] lists node %s %d times. Edit it in Advanced.", old, len(es)), File: FileRpt, Line: es[0].Num})
	}
	e := es[0]
	from := e.Key + " = " + e.Value
	re := regexp.MustCompile(`/` + regexp.QuoteMeta(old) + `(,|$)`)
	e.SetValue(re.ReplaceAllString(e.Value, "/"+node+"${1}"))
	e.SetKey(node)
	ed.touched[FileRpt] = true
	ed.out = append(ed.out, Applied{Key: node, File: FileRpt, Section: "nodes", Action: "set", From: strings.TrimSpace(from), To: e.Key + " = " + e.Value, Note: "[nodes] entry"})
	return nil
}
