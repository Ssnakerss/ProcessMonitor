package web

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const sessionCookieName = "pc_session"

type userContextKey int

const userKey userContextKey = 0

type userSession struct {
	username string
	created  time.Time
}

var (
	sessionStore = make(map[string]*userSession)
	sessionMu    sync.RWMutex
)

func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func setSession(id string, u *userSession) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	sessionStore[id] = u
}

func getSession(id string) (*userSession, bool) {
	sessionMu.RLock()
	defer sessionMu.RUnlock()
	u, ok := sessionStore[id]
	return u, ok
}

func deleteSession(id string) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	delete(sessionStore, id)
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		sess, ok := getSession(cookie.Value)
		if !ok || time.Since(sess.created) > 24*time.Hour {
			http.SetCookie(w, &http.Cookie{
				Name:   sessionCookieName,
				Value:  "",
				Path:   "/",
				MaxAge: -1,
			})
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		ctx := context.WithValue(r.Context(), userKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
