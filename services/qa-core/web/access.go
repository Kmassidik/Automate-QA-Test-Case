package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
)

// Access gates the whole tool behind a single shared code (PRD §5.5). The cookie
// stores sha256(code) — never the code itself — and is compared in constant time.
// No accounts, no sessions; this matches the reference's beta-code gate.
type Access struct {
	expectedToken string
}

const accessCookie = "qa_access"

func NewAccess(code string) *Access {
	return &Access{expectedToken: token(code)}
}

func token(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// Check reports whether the request carries a valid access cookie.
func (a *Access) Check(r *http.Request) bool {
	c, err := r.Cookie(accessCookie)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.expectedToken)) == 1
}

// Grant validates a submitted code and, on success, sets the cookie.
func (a *Access) Grant(w http.ResponseWriter, submitted string) bool {
	if subtle.ConstantTimeCompare([]byte(token(submitted)), []byte(a.expectedToken)) != 1 {
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     accessCookie,
		Value:    a.expectedToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
	})
	return true
}

// Revoke clears the access cookie (sign out): an expired, empty cookie that the
// browser drops immediately.
func (a *Access) Revoke(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     accessCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// Middleware protects a handler, redirecting unauthenticated browsers to the
// access page (and returning 401 for non-GET, e.g. htmx posts).
func (a *Access) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Check(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet {
			http.Redirect(w, r, "/access", http.StatusSeeOther)
			return
		}
		http.Error(w, "access code required", http.StatusUnauthorized)
	})
}
