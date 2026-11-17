package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/store"
)

// Cookie and header names.
const (
	CookieName   = "aslcp_session"
	CSRFHeader   = "X-ASLCP"
	SessionTTL   = 30 * 24 * time.Hour
	setupCodeLen = 10
)

// Errors.
var (
	ErrBadCredentials = errors.New("auth: bad credentials")
	ErrRateLimited    = errors.New("auth: too many attempts")
	ErrBadSetupCode   = errors.New("auth: bad setup code")
	ErrSetupDone      = errors.New("auth: setup already completed")
)

// RateLimitError carries the retry delay.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return ErrRateLimited.Error() }
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

// Service ties the store, limiter and setup code together.
type Service struct {
	Store   *store.Store
	Limiter *Limiter
	DataDir string
	Now     func() time.Time
	// DevMode drops the Secure cookie flag (plain-HTTP development).
	DevMode bool

	dummyHash string

	mu        sync.Mutex
	setupCode string
}

// NewService creates a Service.
func NewService(st *store.Store, dataDir string) (*Service, error) {
	d, err := HashPassword("aslcp-dummy-password")
	if err != nil {
		return nil, err
	}
	return &Service{Store: st, Limiter: NewLimiter(), DataDir: dataDir, Now: time.Now, dummyHash: d}, nil
}

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// NewToken returns a random session token.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// StartSession creates a session for the user and returns the raw token.
func (s *Service) StartSession(userID int64) (string, error) {
	tok, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := s.Store.CreateSession(hashToken(tok), userID, s.Now().Add(SessionTTL)); err != nil {
		return "", err
	}
	return tok, nil
}

// Validate returns the user for a raw token and slides the expiry.
func (s *Service) Validate(tok string) (store.User, error) {
	if tok == "" {
		return store.User{}, store.ErrNotFound
	}
	h := hashToken(tok)
	now := s.Now()
	se, u, err := s.Store.SessionByHash(h, now)
	if err != nil {
		return store.User{}, err
	}
	// Only write when it matters (about hourly) to spare the SD card.
	if now.Sub(se.LastSeen) > time.Hour {
		_ = s.Store.TouchSession(h, now, now.Add(SessionTTL))
	}
	return u, nil
}

// EndSession deletes the session for a raw token.
func (s *Service) EndSession(tok string) error { return s.Store.DeleteSession(hashToken(tok)) }

// SessionHash exposes the stored hash for a raw token (to keep one session).
func SessionHash(tok string) string { return hashToken(tok) }

// Login verifies credentials with rate limiting and starts a session.
func (s *Service) Login(ip, username, password string) (string, store.User, error) {
	key := Key(ip, username)
	if ok, retry := s.Limiter.Allow(key); !ok {
		return "", store.User{}, &RateLimitError{RetryAfter: retry}
	}
	if len(password) > MaxPasswordLen {
		s.Limiter.Fail(key)
		return "", store.User{}, ErrBadCredentials
	}
	u, err := s.Store.UserByName(username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			VerifyPassword(password, s.dummyHash) // equalise timing
			s.Limiter.Fail(key)
			return "", store.User{}, ErrBadCredentials
		}
		return "", store.User{}, err
	}
	ok, err := VerifyPassword(password, u.PWHash)
	if err != nil || !ok {
		s.Limiter.Fail(key)
		return "", store.User{}, ErrBadCredentials
	}
	s.Limiter.Reset(key)
	tok, err := s.StartSession(u.ID)
	if err != nil {
		return "", store.User{}, err
	}
	u.PWHash = ""
	return tok, u, nil
}

// Setup code -------------------------------------------------------------

func (s *Service) setupPath() string { return filepath.Join(s.DataDir, "setup-code") }

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomCode() (string, error) {
	b := make([]byte, setupCodeLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, setupCodeLen)
	for i, v := range b {
		out[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	return string(out), nil
}

// EnsureSetupCode creates the setup code file when no users exist, and
// removes a stale one otherwise. It returns the code ("" when not needed).
func (s *Service) EnsureSetupCode() (string, error) {
	n, err := s.Store.CountUsers()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > 0 {
		s.setupCode = ""
		os.Remove(s.setupPath())
		return "", nil
	}
	code, err := randomCode()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.DataDir, 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(s.setupPath(), []byte(code+"\n"), 0o600); err != nil {
		return "", err
	}
	_ = os.Chmod(s.setupPath(), 0o600)
	s.setupCode = code
	return code, nil
}

// SetupNeeded reports whether no users exist.
func (s *Service) SetupNeeded() (bool, error) {
	n, err := s.Store.CountUsers()
	return n == 0, err
}

// CompleteSetup validates the code and creates the first admin, logging in.
func (s *Service) CompleteSetup(ip, code, username, password string) (string, store.User, error) {
	key := Key(ip, "setup")
	if ok, retry := s.Limiter.Allow(key); !ok {
		return "", store.User{}, &RateLimitError{RetryAfter: retry}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	need, err := s.SetupNeeded()
	if err != nil {
		return "", store.User{}, err
	}
	if !need {
		return "", store.User{}, ErrSetupDone
	}
	if s.setupCode == "" || subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(code))), []byte(s.setupCode)) != 1 {
		s.Limiter.Fail(key)
		return "", store.User{}, ErrBadSetupCode
	}
	if err := ValidateUsername(username); err != nil {
		return "", store.User{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return "", store.User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", store.User{}, err
	}
	u, err := s.Store.CreateUser(username, hash, store.RoleAdmin)
	if err != nil {
		return "", store.User{}, err
	}
	s.setupCode = ""
	os.Remove(s.setupPath())
	s.Limiter.Reset(key)
	tok, err := s.StartSession(u.ID)
	return tok, u, err
}

// CheckSetupCode verifies the setup code without consuming it, with the same
// rate limiting as CompleteSetup. It is for operations a fresh node allows
// before the first admin exists (restoring a backup); ErrSetupDone is
// returned once users exist.
func (s *Service) CheckSetupCode(ip, code string) error {
	key := Key(ip, "setup")
	if ok, retry := s.Limiter.Allow(key); !ok {
		return &RateLimitError{RetryAfter: retry}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	need, err := s.SetupNeeded()
	if err != nil {
		return err
	}
	if !need {
		return ErrSetupDone
	}
	if s.setupCode == "" || subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(code))), []byte(s.setupCode)) != 1 {
		s.Limiter.Fail(key)
		return ErrBadSetupCode
	}
	return nil
}

// ValidateUsername enforces 1-32 chars of letters, digits, . _ -.
func ValidateUsername(u string) error {
	if len(u) < 1 || len(u) > 32 {
		return errors.New("username must be 1-32 characters")
	}
	for _, r := range u {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return errors.New("username may contain only letters, digits, '.', '_' and '-'")
		}
	}
	return nil
}

// HTTP helpers -----------------------------------------------------------

type ctxKey struct{}

// UserFrom returns the authenticated user from a request context.
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(store.User)
	return u, ok
}

// WithUser stores a user in ctx.
func WithUser(ctx context.Context, u store.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// TokenFrom reads the session token cookie.
func TokenFrom(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// Authenticate returns the session user of a request, if any.
func (s *Service) Authenticate(r *http.Request) (store.User, bool) {
	u, err := s.Validate(TokenFrom(r))
	if err != nil {
		return store.User{}, false
	}
	return u, true
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// SetCookie writes the session cookie.
func (s *Service) SetCookie(w http.ResponseWriter, r *http.Request, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: !s.DevMode && isHTTPS(r), Expires: s.Now().Add(SessionTTL), MaxAge: int(SessionTTL / time.Second),
	})
}

// ClearCookie expires the session cookie.
func (s *Service) ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: !s.DevMode && isHTTPS(r), MaxAge: -1, Expires: time.Unix(0, 0),
	})
}

// RoleRank orders roles.
func RoleRank(role string) int {
	switch role {
	case store.RoleAdmin:
		return 3
	case store.RoleOperator:
		return 2
	case store.RoleViewer:
		return 1
	}
	return 0
}

// ClientIP returns the peer IP (RemoteAddr only; proxies are not trusted).
func ClientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// CheckCSRF validates the CSRF header and Origin of a non-safe request.
// extraOrigins are additional allowed origins (e.g. the Vite dev server).
// It returns "" when the request passes, else a reason.
func CheckCSRF(r *http.Request, extraOrigins []string) string {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return ""
	}
	if r.Header.Get(CSRFHeader) != "1" {
		return "missing " + CSRFHeader + " header"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return ""
	}
	for _, o := range extraOrigins {
		if origin == o {
			return ""
		}
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || !strings.EqualFold(u.Host, r.Host) {
		return "cross-origin request rejected"
	}
	return ""
}
