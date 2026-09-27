package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testGoogleAuth(t *testing.T) (*googleAuth, *rsa.PrivateKey) {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	file := filepath.Join(dir, "google.json")
	os.WriteFile(file, []byte(`{"web":{"client_id":"client.apps.googleusercontent.com","client_secret":"private","redirect_uris":["https://386gpt.truvis.co/api/callback/google/oauth"]}}`), 0600)
	a, err := newGoogleAuth(store.db, file, "https://386gpt.truvis.co")
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	a.keys.keys = map[string]*rsa.PublicKey{"test": &key.PublicKey}
	a.keys.fetched = time.Now()
	return a, key
}
func beginLogin(t *testing.T, a *googleAuth) (url.Values, *http.Cookie) {
	t.Helper()
	w := httptest.NewRecorder()
	a.protect(http.NotFoundHandler()).ServeHTTP(w, httptest.NewRequest("GET", "/api/auth/google", nil))
	if w.Code != 302 {
		t.Fatal(w.Body.String())
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Domain != "" || cookie.Path != "/" {
		t.Fatal("insecure state cookie")
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("scope") != "openid email" || q.Get("nonce") == "" {
		t.Fatal("missing OAuth protections")
	}
	return q, cookie
}
func completeLogin(a *googleAuth, q url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", oauthCallback+"?state="+url.QueryEscape(q.Get("state"))+"&code=one-time-code", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.protect(http.NotFoundHandler()).ServeHTTP(w, r)
	return w
}
func TestGoogleOAuthSessionAndLogout(t *testing.T) {
	a, key := testGoogleAuth(t)
	notified := 0
	a.onLogin = func(id accountIdentity) {
		notified++
		if id.ID != identityID("https://accounts.google.com", "user", "google-user") {
			t.Error("wrong login account")
		}
	}
	q, cookie := beginLogin(t, a)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if tokenHash(r.Form.Get("code_verifier")) != q.Get("code_challenge") || r.Form.Get("redirect_uri") != a.callback || r.Form.Get("client_secret") != a.clientSecret {
			t.Error("invalid token exchange")
		}
		c := googleClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: "https://accounts.google.com", Subject: "google-user", Audience: jwt.ClaimStrings{a.clientID}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, Email: "user@gmail.com", Verified: true, Nonce: q.Get("nonce")}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
		token.Header["kid"] = "test"
		raw, _ := token.SignedString(key)
		json.NewEncoder(w).Encode(map[string]string{"id_token": raw})
	}))
	defer provider.Close()
	a.tokenURL = provider.URL
	wrong := *cookie
	wrong.Value = "other-browser"
	if w := completeLogin(a, q, &wrong); w.Code != 400 {
		t.Fatal("login CSRF accepted")
	}
	w := completeLogin(a, q, cookie)
	if w.Code != 303 {
		t.Fatalf("callback: %d %s", w.Code, w.Body.String())
	}
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == a.sessionCookie {
			session = c
		}
	}
	if session == nil || !session.Secure || !session.HttpOnly || session.Domain != "" {
		t.Fatal("missing secure session cookie")
	}
	if w := completeLogin(a, q, cookie); w.Code != 400 {
		t.Fatal("callback replay accepted")
	}
	if notified != 1 {
		t.Fatal("valid login must trigger exactly one environment check", notified)
	}
	handler := a.protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Context().Value(identityKey{}).(accountIdentity)
		if id.ID != identityID("https://accounts.google.com", "user", "google-user") || id.Email != "user@gmail.com" {
			t.Error("wrong identity")
		}
		check := r.Context().Value(sessionCheckKey{}).(func() bool)
		if !check() {
			t.Error("valid session rejected")
		}
		w.WriteHeader(204)
	}))
	call := func(method, path string, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.AddCookie(session)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if call("GET", "/api/account", "").Code != 204 {
		t.Fatal("session failed")
	}
	if call("POST", "/api/auth/logout", "https://evil.example").Code != 403 {
		t.Fatal("logout CSRF allowed")
	}
	if call("POST", "/api/threads", "https://evil.example").Code != 403 {
		t.Fatal("cross-site mutation allowed")
	}
	if call("POST", "/api/auth/logout", a.origin).Code != 204 {
		t.Fatal("logout failed")
	}
	if call("GET", "/api/account", "").Code != 401 {
		t.Fatal("revoked session accepted")
	}
	var rawCount int
	a.db.QueryRow(`SELECT COUNT(*) FROM login_sessions WHERE hash=?`, session.Value).Scan(&rawCount)
	if rawCount != 0 {
		t.Fatal("raw bearer stored")
	}
}
func TestGoogleRejectsInvalidIdentity(t *testing.T) {
	for _, variant := range []string{"nonce", "audience", "issuer", "unverified", "expired", "azp", "no-subject"} {
		t.Run(variant, func(t *testing.T) {
			a, key := testGoogleAuth(t)
			q, cookie := beginLogin(t, a)
			c := googleClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: "https://accounts.google.com", Subject: "subject", Audience: jwt.ClaimStrings{a.clientID}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, Email: "user@gmail.com", Verified: true, Nonce: q.Get("nonce")}
			switch variant {
			case "nonce":
				c.Nonce = "wrong"
			case "audience":
				c.Audience = jwt.ClaimStrings{"wrong"}
			case "issuer":
				c.Issuer = "https://evil.example"
			case "unverified":
				c.Verified = false
			case "expired":
				c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))
			case "azp":
				c.AuthorizedParty = "other-client"
			case "no-subject":
				c.Subject = ""
			}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				token := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
				token.Header["kid"] = "test"
				raw, _ := token.SignedString(key)
				json.NewEncoder(w).Encode(map[string]string{"id_token": raw})
			}))
			defer provider.Close()
			a.tokenURL = provider.URL
			if w := completeLogin(a, q, cookie); w.Code != 401 {
				t.Fatalf("invalid identity accepted: %d", w.Code)
			}
		})
	}
}
func TestOAuthIgnoresForgedEdgeIdentity(t *testing.T) {
	a, _ := testGoogleAuth(t)
	r := httptest.NewRequest("GET", "/api/threads", nil)
	r.Header.Set("Cf-Access-Jwt-Assertion", "forged")
	r.Header.Set("Cf-Access-Authenticated-User-Email", "mauriciootta@gmail.com")
	w := httptest.NewRecorder()
	a.protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("identity headers accepted") })).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
