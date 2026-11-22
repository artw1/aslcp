package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// DVFavorite is a saved digital modes target (a reflector, a talkgroup), shared by
// everybody on the node. Mode is BM, DMR, YSF, P25 or NXDN; the API validates
// Value per mode.
type DVFavorite struct {
	ID    int64  `json:"id"`
	Mode  string `json:"mode"`
	Value string `json:"value"`
	Name  string `json:"name"`
	Sort  int    `json:"sort"`
}

var (
	// ErrDuplicate: the mode and value are saved already.
	ErrDuplicate = errors.New("store: duplicate")
	// ErrLimit: too many entries.
	ErrLimit = errors.New("store: limit reached")
)

// MaxDVFavoritesPerMode caps the favourites of one mode.
const MaxDVFavoritesPerMode = 50

// ListDVFavorites returns all favourites ordered by mode, sort, id.
func (s *Store) ListDVFavorites() ([]DVFavorite, error) {
	rows, err := s.db.Query(`SELECT id,mode,value,name,sort FROM dv_favorites ORDER BY mode, sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DVFavorite{}
	for rows.Next() {
		var f DVFavorite
		if err := rows.Scan(&f.ID, &f.Mode, &f.Value, &f.Name, &f.Sort); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetDVFavorite returns one favourite or ErrNotFound.
func (s *Store) GetDVFavorite(id int64) (DVFavorite, error) {
	var f DVFavorite
	err := s.db.QueryRow(`SELECT id,mode,value,name,sort FROM dv_favorites WHERE id=?`, id).
		Scan(&f.ID, &f.Mode, &f.Value, &f.Name, &f.Sort)
	if errors.Is(err, sql.ErrNoRows) {
		return DVFavorite{}, ErrNotFound
	}
	return f, err
}

// AddDVFavorite appends a favourite at the end of its mode. ErrDuplicate for
// the same mode and value, ErrLimit past MaxDVFavoritesPerMode.
func (s *Store) AddDVFavorite(mode, value, name string) (DVFavorite, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM dv_favorites WHERE mode=?`, mode).Scan(&n); err != nil {
		return DVFavorite{}, err
	}
	if n >= MaxDVFavoritesPerMode {
		return DVFavorite{}, ErrLimit
	}
	res, err := s.db.Exec(`INSERT INTO dv_favorites(mode,value,name,sort,created)
		VALUES (?,?,?,(SELECT COALESCE(MAX(sort),-1)+1 FROM dv_favorites WHERE mode=?),?)`,
		mode, value, name, mode, time.Now().Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return DVFavorite{}, ErrDuplicate
		}
		return DVFavorite{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetDVFavorite(id)
}

// UpdateDVFavorite changes the non-nil fields.
func (s *Store) UpdateDVFavorite(id int64, name *string, sort *int) (DVFavorite, error) {
	f, err := s.GetDVFavorite(id)
	if err != nil {
		return DVFavorite{}, err
	}
	if name != nil {
		f.Name = *name
	}
	if sort != nil {
		f.Sort = *sort
	}
	_, err = s.db.Exec(`UPDATE dv_favorites SET name=?, sort=? WHERE id=?`, f.Name, f.Sort, id)
	return f, err
}

// DeleteDVFavorite removes a favourite or returns ErrNotFound.
func (s *Store) DeleteDVFavorite(id int64) error {
	res, err := s.db.Exec(`DELETE FROM dv_favorites WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
