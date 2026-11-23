package configsvc

import (
	"context"
	"io/fs"
	"path"
	"strings"

	"github.com/artw1/aslcp/internal/astconf"
	"github.com/artw1/aslcp/internal/helper"
)

// helperFS adapts the helper's read-only config ops to astconf.FS so that
// includes resolve.
type helperFS struct {
	ctx context.Context
	h   Helper
}

func (f helperFS) rel(p string) string { return strings.TrimPrefix(p, helper.DefaultConfigRoot+"/") }

func (f helperFS) ReadFile(p string) ([]byte, error) {
	c, err := f.h.ConfigRead(f.ctx, f.rel(p))
	if err != nil {
		if strings.Contains(err.Error(), helper.ErrNoSuchFile) {
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	return []byte(c.Content), nil
}

func (f helperFS) Glob(pattern string) ([]string, error) {
	list, err := f.h.ConfigList(f.ctx)
	if err != nil {
		return nil, err
	}
	pat := f.rel(pattern)
	var out []string
	for _, e := range list {
		if ok, _ := path.Match(pat, e.Path); ok {
			out = append(out, e.Path)
		}
	}
	return out, nil
}

// Tree loads file with all of its includes (read-only).
func (s *Service) Tree(ctx context.Context, file string) (*astconf.Tree, error) {
	return astconf.LoadTree(helperFS{ctx: ctx, h: s.H}, "", file)
}

// FS returns a read-only view of /etc/asterisk for astconf (includes
// resolve through the helper). Paths are relative to /etc/asterisk.
func (s *Service) FS(ctx context.Context) astconf.FS { return helperFS{ctx: ctx, h: s.H} }
