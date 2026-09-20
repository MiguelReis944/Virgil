package dashboard

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const cookieName = "virgil_session"
const sessionTTL = 24 * time.Hour

var (
	sessionsMu sync.Mutex
	sessions   = map[string]time.Time{}
)

// IsAuthed returns true if the request carries a valid session cookie, or if
// no password is configured (open access).
func IsAuthed(r *http.Request, password string) bool {
	if password == "" {
		return true
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	exp, ok := sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(sessions, c.Value)
		return false
	}
	return true
}

// Login verifies the input password and, if correct, sets a session cookie.
func Login(w http.ResponseWriter, password, input string) (bool, error) {
	if subtle.ConstantTimeCompare([]byte(password), []byte(input)) != 1 {
		return false, nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return false, err
	}
	token := hex.EncodeToString(b)

	sessionsMu.Lock()
	sessions[token] = time.Now().Add(sessionTTL)
	sessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return true, nil
}

// Logout deletes the session and clears the cookie.
func Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		sessionsMu.Lock()
		delete(sessions, c.Value)
		sessionsMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}
