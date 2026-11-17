package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/store"
)

func TestHashVerify(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=32768,t=2,p=2$") {
		t.Fatal(h)
	}
	if ok, err := VerifyPassword("correct horse", h); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := VerifyPassword("wrong", h); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse")
	if h == h2 {
		t.Fatal("salt not random")
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=1,t=1$x$y", "$argon2id$v=19$m=99999999,t=1,p=1$AAAA$AAAA", "$bcrypt$a$b$c$d"} {
		if _, err := VerifyPassword("x", bad); err == nil {
			t.Fatalf("bad hash accepted: %q", bad)
		}
	}
}

func TestValidate(t *testing.T) {
	if ValidatePassword("short") == nil || ValidatePassword(strings.Repeat("a", 300)) == nil || ValidatePassword("longenough") != nil {
		t.Fatal("password rules")
	}
	if ValidateUsername("") == nil || ValidateUsername("a b") == nil || ValidateUsername("ok.user-1") != nil {
		t.Fatal("username rules")
	}
}

func newSvc(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := NewService(st, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoginSessionAndLimit(t *testing.T) {
	s := newSvc(t)
	now := time.Now()
	s.Now = func() time.Time { return now }
	s.Limiter.Now = s.Now
	h, _ := HashPassword("password123")
	s.Store.CreateUser("alice", h, store.RoleOperator)

	tok, u, err := s.Login("1.2.3.4", "Alice", "password123")
	if err != nil || u.Username != "alice" || u.PWHash != "" {
		t.Fatal(err, u)
	}
	got, err := s.Validate(tok)
	if err != nil || got.Role != store.RoleOperator {
		t.Fatal(err)
	}
	// token itself must not be stored
	se, _, _ := s.Store.SessionByHash(SessionHash(tok), now)
	if se.TokenHash == tok || se.TokenHash == "" {
		t.Fatal("token stored in clear")
	}
	// sliding expiry: after 2h, touched; after 29 days still valid.
	now = now.Add(2 * time.Hour)
	if _, err := s.Validate(tok); err != nil {
		t.Fatal(err)
	}
	now = now.Add(29 * 24 * time.Hour)
	if _, err := s.Validate(tok); err != nil {
		t.Fatal("sliding expiry failed:", err)
	}
	now = now.Add(31 * 24 * time.Hour)
	if _, err := s.Validate(tok); err == nil {
		t.Fatal("expired session valid")
	}
	if _, err := s.Validate(""); err == nil {
		t.Fatal("empty token valid")
	}
	s.EndSession(tok)

	// rate limit
	for i := 0; i < 5; i++ {
		if _, _, err := s.Login("9.9.9.9", "alice", "nope"); err != ErrBadCredentials {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	_, _, err = s.Login("9.9.9.9", "alice", "password123")
	var rl *RateLimitError
	if err == nil || !asRL(err, &rl) || rl.RetryAfter <= 0 {
		t.Fatalf("want rate limit, got %v", err)
	}
	// another IP unaffected
	if _, _, err := s.Login("8.8.8.8", "alice", "password123"); err != nil {
		t.Fatal(err)
	}
	// window passes
	now = now.Add(6 * time.Minute)
	if _, _, err := s.Login("9.9.9.9", "alice", "password123"); err != nil {
		t.Fatal(err)
	}
	// unknown user
	if _, _, err := s.Login("7.7.7.7", "ghost", "x"); err != ErrBadCredentials {
		t.Fatal(err)
	}
}

func asRL(err error, out **RateLimitError) bool {
	e, ok := err.(*RateLimitError)
	if ok {
		*out = e
	}
	return ok
}

func TestSetupFlow(t *testing.T) {
	s := newSvc(t)
	code, err := s.EnsureSetupCode()
	if err != nil || len(code) != setupCodeLen {
		t.Fatal(code, err)
	}
	p := filepath.Join(s.DataDir, "setup-code")
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatal(err, fi)
	}
	if b, _ := os.ReadFile(p); strings.TrimSpace(string(b)) != code {
		t.Fatal("file content")
	}
	if _, _, err := s.CompleteSetup("1.1.1.1", "WRONGCODE1", "admin", "password123"); err != ErrBadSetupCode {
		t.Fatal(err)
	}
	if _, _, err := s.CompleteSetup("1.1.1.1", code, "bad name", "password123"); err == nil {
		t.Fatal("bad username accepted")
	}
	if _, _, err := s.CompleteSetup("1.1.1.1", code, "admin", "short"); err == nil {
		t.Fatal("short password accepted")
	}
	tok, u, err := s.CompleteSetup("1.1.1.1", strings.ToLower(code), "admin", "password123")
	if err != nil || u.Role != store.RoleAdmin || tok == "" {
		t.Fatal(err, u)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("setup code file not removed")
	}
	if _, _, err := s.CompleteSetup("1.1.1.1", code, "admin2", "password123"); err != ErrSetupDone {
		t.Fatal(err)
	}
	if c, _ := s.EnsureSetupCode(); c != "" {
		t.Fatal("code regenerated with users present")
	}
}

func TestSetupRateLimited(t *testing.T) {
	s := newSvc(t)
	s.EnsureSetupCode()
	for i := 0; i < 5; i++ {
		s.CompleteSetup("2.2.2.2", "AAAAAAAAAA", "a", "password123")
	}
	if _, _, err := s.CompleteSetup("2.2.2.2", "AAAAAAAAAA", "a", "password123"); err == nil || err == ErrBadSetupCode {
		t.Fatalf("want rate limit: %v", err)
	}
}

func TestCSRF(t *testing.T) {
	mk := func(method, hdr, origin string) *http.Request {
		r := httptest.NewRequest(method, "http://aslcp.local/api/x", nil)
		if hdr != "" {
			r.Header.Set(CSRFHeader, hdr)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	dev := []string{"http://localhost:5173"}
	cases := []struct {
		name string
		r    *http.Request
		ok   bool
	}{
		{"get needs nothing", mk("GET", "", "http://evil.example"), true},
		{"post without header", mk("POST", "", ""), false},
		{"post wrong header value", mk("POST", "0", ""), false},
		{"post header no origin", mk("POST", "1", ""), true},
		{"post same origin", mk("POST", "1", "http://aslcp.local"), true},
		{"post cross origin", mk("POST", "1", "http://evil.example"), false},
		{"post dev origin", mk("DELETE", "1", "http://localhost:5173"), true},
		{"post null origin", mk("PUT", "1", "null"), false},
	}
	for _, c := range cases {
		got := CheckCSRF(c.r, dev) == ""
		if got != c.ok {
			t.Errorf("%s: got %v want %v", c.name, got, c.ok)
		}
	}
	// dev origin not allowed when not configured
	if CheckCSRF(mk("POST", "1", "http://localhost:5173"), nil) == "" {
		t.Error("dev origin allowed without config")
	}
}

func TestCookieFlags(t *testing.T) {
	s := newSvc(t)
	r := httptest.NewRequest("GET", "https://x/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	s.SetCookie(w, r, "tok")
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("%+v", c)
	}
	s.DevMode = true
	w = httptest.NewRecorder()
	s.SetCookie(w, r, "tok")
	if w.Result().Cookies()[0].Secure {
		t.Fatal("secure in dev mode")
	}
	w = httptest.NewRecorder()
	httptest.NewRequest("GET", "http://x/", nil)
	s.DevMode = false
	s.SetCookie(w, httptest.NewRequest("GET", "http://x/", nil), "tok")
	if w.Result().Cookies()[0].Secure {
		t.Fatal("secure over plain http")
	}
}
