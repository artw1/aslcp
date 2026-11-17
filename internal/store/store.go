// Package store is the SQLite persistence layer (users, sessions,
// favorites, settings).
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver
)

// Errors returned by the store.
var (
	ErrNotFound = errors.New("store: not found")
	ErrExists   = errors.New("store: already exists")
)

// Roles.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// ValidRole reports whether r is a known role.
func ValidRole(r string) bool { return r == RoleAdmin || r == RoleOperator || r == RoleViewer }

// Store wraps the database.
type Store struct{ db *sql.DB }

// User is an account. PWHash is only filled by lookups that need it.
type User struct {
	ID       int64
	Username string
	Role     string
	PWHash   string
	Created  time.Time
}

// Session is a login session; Token is never stored, only its hash.
type Session struct {
	TokenHash string
	UserID    int64
	Expires   time.Time
	Created   time.Time
	LastSeen  time.Time
}

// Favorite is a saved node.
type Favorite struct {
	ID       int64
	Node     string
	Label    string
	Group    string
	Position int
}

var migrations = []string{
	`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE COLLATE NOCASE,
		pw_hash TEXT NOT NULL,
		role TEXT NOT NULL CHECK (role IN ('admin','operator','viewer')),
		created INTEGER NOT NULL
	);
	CREATE TABLE sessions (
		token_hash TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		expires INTEGER NOT NULL,
		created INTEGER NOT NULL,
		last_seen INTEGER NOT NULL
	);
	CREATE INDEX sessions_user ON sessions(user_id);
	CREATE TABLE favorites (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		node TEXT NOT NULL,
		label TEXT NOT NULL DEFAULT '',
		grp TEXT NOT NULL DEFAULT '',
		position INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE settings (k TEXT PRIMARY KEY, v TEXT NOT NULL);`,
	// 2: recent activity (link and local-receiver keyups) for the public page.
	`CREATE TABLE activity (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		node TEXT NOT NULL,
		link TEXT NOT NULL,
		start INTEGER NOT NULL,
		duration_ms INTEGER NOT NULL,
		kerchunk INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX activity_start ON activity(start);
	CREATE INDEX activity_node ON activity(node, start);`,
	// 3: audit log of privileged actions (never holds secret values).
	`CREATE TABLE audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts INTEGER NOT NULL,
		username TEXT NOT NULL,
		action TEXT NOT NULL,
		detail TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX audit_ts ON audit(ts);`,
}

// Open opens (creating if needed) the database at path and migrates it.
func Open(path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // serialises access; the workload is tiny
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Version returns the current schema version.
func (s *Store) Version() (int, error) {
	var v int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&v)
	return v, err
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	cur, err := s.Version()
	if err != nil {
		return err
	}
	if cur > len(migrations) {
		return fmt.Errorf("store: database schema %d is newer than this build (%d)", cur, len(migrations))
	}
	for i := cur; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func ts(t time.Time) int64     { return t.Unix() }
func fromTS(n int64) time.Time { return time.Unix(n, 0).UTC() }

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// Users -----------------------------------------------------------------

// CreateUser inserts a user.
func (s *Store) CreateUser(username, pwHash, role string) (User, error) {
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO users(username,pw_hash,role,created) VALUES (?,?,?,?)`, username, pwHash, role, ts(now))
	if err != nil {
		if isUnique(err) {
			return User{}, ErrExists
		}
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, Role: role, Created: fromTS(ts(now))}, nil
}

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var c int64
	err := row.Scan(&u.ID, &u.Username, &u.PWHash, &u.Role, &c)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	u.Created = fromTS(c)
	return u, err
}

const userCols = `id,username,pw_hash,role,created`

// UserByName looks a user up case-insensitively (includes PWHash).
func (s *Store) UserByName(name string) (User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username = ?`, name))
}

// UserByID looks a user up by id (includes PWHash).
func (s *Store) UserByID(id int64) (User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// ListUsers returns all users by id.
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		u.PWHash = ""
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers returns the number of users.
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CountAdmins returns the number of admin users.
func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&n)
	return n, err
}

// UpdateUser changes the role and/or password hash (nil leaves unchanged).
func (s *Store) UpdateUser(id int64, role, pwHash *string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if role != nil {
		res, err := tx.Exec(`UPDATE users SET role=? WHERE id=?`, *role, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
	}
	if pwHash != nil {
		res, err := tx.Exec(`UPDATE users SET pw_hash=? WHERE id=?`, *pwHash, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
	}
	return tx.Commit()
}

// DeleteUser removes a user (sessions cascade).
func (s *Store) DeleteUser(id int64) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Sessions --------------------------------------------------------------

// CreateSession stores a session by token hash.
func (s *Store) CreateSession(tokenHash string, userID int64, expires time.Time) error {
	now := ts(time.Now())
	_, err := s.db.Exec(`INSERT INTO sessions(token_hash,user_id,expires,created,last_seen) VALUES (?,?,?,?,?)`,
		tokenHash, userID, ts(expires), now, now)
	return err
}

// SessionByHash returns the session and its user (no PWHash). Expired
// sessions are reported as ErrNotFound.
func (s *Store) SessionByHash(tokenHash string, now time.Time) (Session, User, error) {
	var se Session
	var u User
	var exp, cr, ls, uc int64
	err := s.db.QueryRow(`SELECT s.token_hash,s.user_id,s.expires,s.created,s.last_seen,u.id,u.username,u.role,u.created
		FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires>?`, tokenHash, ts(now)).
		Scan(&se.TokenHash, &se.UserID, &exp, &cr, &ls, &u.ID, &u.Username, &u.Role, &uc)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, User{}, ErrNotFound
	}
	if err != nil {
		return Session{}, User{}, err
	}
	se.Expires, se.Created, se.LastSeen, u.Created = fromTS(exp), fromTS(cr), fromTS(ls), fromTS(uc)
	return se, u, nil
}

// TouchSession extends a session (sliding expiry).
func (s *Store) TouchSession(tokenHash string, now, expires time.Time) error {
	_, err := s.db.Exec(`UPDATE sessions SET last_seen=?, expires=? WHERE token_hash=?`, ts(now), ts(expires), tokenHash)
	return err
}

// DeleteSession removes one session.
func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}

// DeleteUserSessions removes all of a user's sessions, except keep (if non-empty).
func (s *Store) DeleteUserSessions(userID int64, keep string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id=? AND token_hash<>?`, userID, keep)
	return err
}

// DeleteExpiredSessions purges expired sessions.
func (s *Store) DeleteExpiredSessions(now time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE expires<=?`, ts(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Favorites -------------------------------------------------------------

// ListFavorites returns favorites ordered by group, position, id.
func (s *Store) ListFavorites() ([]Favorite, error) {
	rows, err := s.db.Query(`SELECT id,node,label,grp,position FROM favorites ORDER BY grp, position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Favorite{}
	for rows.Next() {
		var f Favorite
		if err := rows.Scan(&f.ID, &f.Node, &f.Label, &f.Group, &f.Position); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetFavorite returns one favorite.
func (s *Store) GetFavorite(id int64) (Favorite, error) {
	var f Favorite
	err := s.db.QueryRow(`SELECT id,node,label,grp,position FROM favorites WHERE id=?`, id).
		Scan(&f.ID, &f.Node, &f.Label, &f.Group, &f.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return Favorite{}, ErrNotFound
	}
	return f, err
}

// AddFavorite appends a favorite at the end.
func (s *Store) AddFavorite(node, label, group string) (Favorite, error) {
	res, err := s.db.Exec(`INSERT INTO favorites(node,label,grp,position)
		VALUES (?,?,?,(SELECT COALESCE(MAX(position),-1)+1 FROM favorites))`, node, label, group)
	if err != nil {
		return Favorite{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetFavorite(id)
}

// UpdateFavorite changes the non-nil fields.
func (s *Store) UpdateFavorite(id int64, node, label, group *string) (Favorite, error) {
	f, err := s.GetFavorite(id)
	if err != nil {
		return Favorite{}, err
	}
	if node != nil {
		f.Node = *node
	}
	if label != nil {
		f.Label = *label
	}
	if group != nil {
		f.Group = *group
	}
	_, err = s.db.Exec(`UPDATE favorites SET node=?, label=?, grp=? WHERE id=?`, f.Node, f.Label, f.Group, id)
	return f, err
}

// DeleteFavorite removes a favorite.
func (s *Store) DeleteFavorite(id int64) error {
	res, err := s.db.Exec(`DELETE FROM favorites WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderFavorites sets positions from the id order. Ids not listed keep
// their relative order after the listed ones. Unknown ids yield ErrNotFound.
func (s *Store) ReorderFavorites(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := map[int64]bool{}
	pos := 0
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		res, err := tx.Exec(`UPDATE favorites SET position=? WHERE id=?`, pos, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		pos++
	}
	rows, err := tx.Query(`SELECT id FROM favorites ORDER BY position, id`)
	if err != nil {
		return err
	}
	var rest []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !seen[id] {
			rest = append(rest, id)
		}
	}
	rows.Close()
	for _, id := range rest {
		if _, err := tx.Exec(`UPDATE favorites SET position=? WHERE id=?`, pos, id); err != nil {
			return err
		}
		pos++
	}
	return tx.Commit()
}

// Settings --------------------------------------------------------------

// GetSetting returns a setting or ErrNotFound.
func (s *Store) GetSetting(k string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT v FROM settings WHERE k=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetSetting upserts a setting.
func (s *Store) SetSetting(k, v string) error {
	_, err := s.db.Exec(`INSERT INTO settings(k,v) VALUES (?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// Activity --------------------------------------------------------------

// Activity is one recorded transmission: a keyup of a link (Link is the link's
// node number) or of the local receiver (Link is "local") on local node Node.
type Activity struct {
	ID       int64
	Node     string
	Link     string
	Start    time.Time
	Duration time.Duration
	Kerchunk bool
}

// AddActivity records a transmission.
func (s *Store) AddActivity(a Activity) error {
	k := 0
	if a.Kerchunk {
		k = 1
	}
	_, err := s.db.Exec(`INSERT INTO activity(node,link,start,duration_ms,kerchunk) VALUES (?,?,?,?,?)`,
		a.Node, a.Link, ts(a.Start), a.Duration.Milliseconds(), k)
	return err
}

// ListActivity returns the newest transmissions of the given local nodes
// (all nodes when none are given), newest first. limit is capped at 500.
func (s *Store) ListActivity(nodes []string, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	q := `SELECT id,node,link,start,duration_ms,kerchunk FROM activity`
	var args []any
	if len(nodes) > 0 {
		q += ` WHERE node IN (?` + strings.Repeat(",?", len(nodes)-1) + `)`
		for _, n := range nodes {
			args = append(args, n)
		}
	}
	q += ` ORDER BY start DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var start, ms, k int64
		if err := rows.Scan(&a.ID, &a.Node, &a.Link, &start, &ms, &k); err != nil {
			return nil, err
		}
		a.Start, a.Duration, a.Kerchunk = fromTS(start), time.Duration(ms)*time.Millisecond, k != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

// PruneActivity deletes rows that started before cutoff and then keeps only
// the newest maxRows rows (maxRows <= 0: no cap). It returns the number of
// rows deleted.
func (s *Store) PruneActivity(cutoff time.Time, maxRows int) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM activity WHERE start < ?`, ts(cutoff))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if maxRows > 0 {
		res, err = s.db.Exec(`DELETE FROM activity WHERE id NOT IN (SELECT id FROM activity ORDER BY start DESC, id DESC LIMIT ?)`, maxRows)
		if err != nil {
			return n, err
		}
		m, _ := res.RowsAffected()
		n += m
	}
	return n, nil
}

// CountActivity returns the number of recorded transmissions.
func (s *Store) CountActivity() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM activity`).Scan(&n)
	return n, err
}

// Audit is one audit-log entry. Detail is a short JSON document; callers
// must never put secret values in it.
type Audit struct {
	ID       int64
	Time     time.Time
	Username string
	Action   string
	Detail   string
}

// AddAudit appends an entry.
func (s *Store) AddAudit(a Audit) error {
	if a.Time.IsZero() {
		a.Time = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO audit(ts,username,action,detail) VALUES (?,?,?,?)`,
		ts(a.Time), a.Username, a.Action, a.Detail)
	return err
}

// ListAudit returns the newest entries first (limit capped at 1000).
func (s *Store) ListAudit(limit int) ([]Audit, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT id,ts,username,action,detail FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Audit{}
	for rows.Next() {
		var a Audit
		var t int64
		if err := rows.Scan(&a.ID, &t, &a.Username, &a.Action, &a.Detail); err != nil {
			return nil, err
		}
		a.Time = fromTS(t)
		out = append(out, a)
	}
	return out, rows.Err()
}
