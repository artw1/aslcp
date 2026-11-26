package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Favorite is a saved target of one mode (an AllStar or EchoLink node, a
// talkgroup, a reflector), shared by everybody on the node.
type Favorite struct {
	ID int64 `json:"id"`
	// Mode is the node mode name (allstar, echolink, bm, ...).
	Mode string `json:"mode"`
	// Target is the node number, talkgroup or reflector on that mode.
	Target   string `json:"target"`
	Label    string `json:"label"`
	Group    string `json:"group"`
	Position int    `json:"position"`
	// Pinned favorites show on the overview page.
	Pinned bool `json:"pinned"`
}

var (
	// ErrDuplicate: the mode and target are saved already.
	ErrDuplicate = errors.New("store: duplicate")
	// ErrLimit: too many entries.
	ErrLimit = errors.New("store: limit reached")
	// ErrMismatch: a reorder did not list exactly the favorites of the mode.
	ErrMismatch = errors.New("store: the ids do not match the favorites")
)

// MaxFavoritesPerMode caps the favorites of one mode.
const MaxFavoritesPerMode = 200

const favCols = `id,mode,target,label,grp,position,pinned`

func scanFavorite(r interface{ Scan(...any) error }) (Favorite, error) {
	var f Favorite
	var pin int
	err := r.Scan(&f.ID, &f.Mode, &f.Target, &f.Label, &f.Group, &f.Position, &pin)
	f.Pinned = pin != 0
	return f, err
}

// ListFavorites returns the favorites of mode ("" for every mode), ordered
// by mode, position and id (the UI groups them).
func (s *Store) ListFavorites(mode string) ([]Favorite, error) {
	q := `SELECT ` + favCols + ` FROM favorites`
	var args []any
	if mode != "" {
		q += ` WHERE mode=?`
		args = append(args, mode)
	}
	rows, err := s.db.Query(q+` ORDER BY mode, position, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list favorites: %w", err)
	}
	defer rows.Close()
	out := []Favorite{}
	for rows.Next() {
		f, err := scanFavorite(rows)
		if err != nil {
			return nil, fmt.Errorf("list favorites: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetFavorite returns one favorite or ErrNotFound.
func (s *Store) GetFavorite(id int64) (Favorite, error) {
	f, err := scanFavorite(s.db.QueryRow(`SELECT `+favCols+` FROM favorites WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Favorite{}, ErrNotFound
	}
	return f, err
}

// AddFavorite appends a favorite at the end of its mode. It returns
// ErrDuplicate for a saved mode and target, ErrLimit past
// MaxFavoritesPerMode.
func (s *Store) AddFavorite(mode, target, label, group string) (Favorite, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM favorites WHERE mode=?`, mode).Scan(&n); err != nil {
		return Favorite{}, fmt.Errorf("add favorite: %w", err)
	}
	if n >= MaxFavoritesPerMode {
		return Favorite{}, ErrLimit
	}
	res, err := s.db.Exec(`INSERT INTO favorites(mode,target,label,grp,position)
		VALUES (?,?,?,?,(SELECT COALESCE(MAX(position),-1)+1 FROM favorites WHERE mode=?))`,
		mode, target, label, group, mode)
	if err != nil {
		if isUnique(err) {
			return Favorite{}, ErrDuplicate
		}
		return Favorite{}, fmt.Errorf("add favorite: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetFavorite(id)
}

// UpdateFavorite changes the fields that are not nil. It returns
// ErrNotFound or ErrDuplicate.
func (s *Store) UpdateFavorite(id int64, target, label, group *string, pinned *bool) (Favorite, error) {
	var sets []string
	var args []any
	add := func(col string, v any) { sets = append(sets, col+"=?"); args = append(args, v) }
	if target != nil {
		add("target", *target)
	}
	if label != nil {
		add("label", *label)
	}
	if group != nil {
		add("grp", *group)
	}
	if pinned != nil {
		add("pinned", b2i(*pinned))
	}
	if len(sets) == 0 {
		return s.GetFavorite(id)
	}
	res, err := s.db.Exec(`UPDATE favorites SET `+strings.Join(sets, ",")+` WHERE id=?`, append(args, id)...)
	if err != nil {
		if isUnique(err) {
			return Favorite{}, ErrDuplicate
		}
		return Favorite{}, fmt.Errorf("update favorite: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Favorite{}, ErrNotFound
	}
	return s.GetFavorite(id)
}

// DeleteFavorite removes a favorite or returns ErrNotFound.
func (s *Store) DeleteFavorite(id int64) error {
	res, err := s.db.Exec(`DELETE FROM favorites WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete favorite: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderFavorites sets the positions of mode's favorites to the order of
// ids. ids must list exactly the favorites of that mode.
func (s *Store) ReorderFavorites(mode string, ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("reorder favorites: %w", err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM favorites WHERE mode=?`, mode).Scan(&n); err != nil {
		return fmt.Errorf("reorder favorites: %w", err)
	}
	if n != len(ids) {
		return ErrMismatch
	}
	seen := map[int64]bool{}
	for i, id := range ids {
		if seen[id] {
			return ErrMismatch
		}
		seen[id] = true
		res, err := tx.Exec(`UPDATE favorites SET position=? WHERE id=? AND mode=?`, i, id, mode)
		if err != nil {
			return fmt.Errorf("reorder favorites: %w", err)
		}
		if k, _ := res.RowsAffected(); k == 0 {
			return ErrMismatch
		}
	}
	return tx.Commit()
}
