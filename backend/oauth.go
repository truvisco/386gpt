package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const oauthCallback = "/api/callback/google/oauth"

type googleClaims struct {
	jwt.RegisteredClaims
	Email           string `json:"email"`
	Verified        bool   `json:"email_verified"`
	Nonce           string `json:"nonce"`
	AuthorizedParty string `json:"azp"`
}
type googleAuth struct {
	onLogin                                                              func(accountIdentity)
	db                                                                   *sql.DB
	clientID, clientSecret, origin, callback, sessionCookie, stateCookie string
	secure                                                               bool
	client                                                               *http.Client
	keys                                                                 *accessVerifier
	authorizationURL, tokenURL                                           string
	smokeID, smokeKey                                                    string
}

func randomToken() (string, error) {
	var b [32]byte
	_, err := rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:]), err
}
func tokenHash(raw string) string {
	b := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func newGoogleAuth(db *sql.DB, clientFile, origin string) (*googleAuth, error) {
	b, err := os.ReadFile(clientFile)
	if err != nil {
		return nil, errors.New("Google OAuth client file is unavailable")
	}
	var config struct {
		Web struct {
			ID        string   `json:"client_id"`
			Secret    string   `json:"client_secret"`
			Redirects []string `json:"redirect_uris"`
		} `json:"web"`
	}
	if json.Unmarshal(b, &config) != nil || config.Web.ID == "" || config.Web.Secret == "" {
		return nil, errors.New("Google web OAuth client configuration is required")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid public origin")
	}
	secure := u.Scheme == "https"
	if !secure && !(u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())) {
		return nil, errors.New("OAuth requires HTTPS or loopback development")
	}
	callback := origin + oauthCallback
	found := false
	for _, r := range config.Web.Redirects {
		if r == callback {
			found = true
		}
	}
	if !found {
		return nil, errors.New("public origin callback is not registered in the OAuth client JSON")
	}
	a := &googleAuth{db: db, clientID: config.Web.ID, clientSecret: config.Web.Secret, origin: origin, callback: callback, secure: secure, sessionCookie: "__Host-386gpt-session", stateCookie: "__Host-386gpt-oauth", client: &http.Client{Timeout: 15 * time.Second}, authorizationURL: "https://accounts.google.com/o/oauth2/v2/auth", tokenURL: "https://oauth2.googleapis.com/token", smokeID: os.Getenv("CF_ACCESS_CLIENT_ID"), smokeKey: os.Getenv("CF_ACCESS_CLIENT_SECRET")}
	a.keys = &accessVerifier{issuer: "https://accounts.google.com", jwksURL: "https://www.googleapis.com/oauth2/v3/certs", client: a.client}
	if !secure {
		a.sessionCookie = "386gpt-session"
		a.stateCookie = "386gpt-oauth"
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS oauth_states (hash TEXT PRIMARY KEY, browser_hash TEXT NOT NULL, verifier TEXT NOT NULL, nonce TEXT NOT NULL, expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS login_sessions (hash TEXT PRIMARY KEY, account_id TEXT NOT NULL, email TEXT NOT NULL, expires INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS login_sessions_expiry ON login_sessions(expires);`)
	return a, err
}
func (a *googleAuth) cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: a.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func (a *googleAuth) login(w http.ResponseWriter, r *http.Request) {
	// Bound outstanding anonymous flows; entries expire in ten minutes.
	now := time.Now().Unix()
	_, _ = a.db.Exec(`DELETE FROM oauth_states WHERE expires<?`, now)
	_, _ = a.db.Exec(`DELETE FROM login_sessions WHERE expires<?`, now)
	state, e := randomToken()
	if e != nil {
		http.Error(w, "Login unavailable", 503)
		return
	}
	browser, e := randomToken()
	if e != nil {
		http.Error(w, "Login unavailable", 503)
		return
	}
	verifier, e := randomToken()
	if e != nil {
		http.Error(w, "Login unavailable", 503)
		return
	}
	nonce, e := randomToken()
	if e != nil {
		http.Error(w, "Login unavailable", 503)
		return
	}
	res, e := a.db.Exec(`INSERT INTO oauth_states SELECT ?,?,?,?,? WHERE (SELECT COUNT(*) FROM oauth_states)<1000`, tokenHash(state), tokenHash(browser), verifier, nonce, now+600)
	if e != nil {
		http.Error(w, "Login unavailable", 503)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		http.Error(w, "Please retry shortly", 429)
		return
	}
	a.cookie(w, a.stateCookie, browser, 600)
	q := url.Values{"client_id": {a.clientID}, "redirect_uri": {a.callback}, "response_type": {"code"}, "scope": {"openid email"}, "state": {state}, "nonce": {nonce}, "code_challenge": {tokenHash(verifier)}, "code_challenge_method": {"S256"}, "prompt": {"select_account"}}
	http.Redirect(w, r, a.authorizationURL+"?"+q.Encode(), http.StatusFound)
}
func (a *googleAuth) callbackHandler(w http.ResponseWriter, r *http.Request) {
	browser, err := r.Cookie(a.stateCookie)
	if err != nil || r.URL.Query().Get("state") == "" {
		http.Error(w, "Invalid login state. Please sign in again.", 400)
		return
	}
	var verifier, nonce string
	// Consumed exactly once and bound to the initiating browser, including on errors.
	err = a.db.QueryRow(`DELETE FROM oauth_states WHERE hash=? AND browser_hash=? AND expires>? RETURNING verifier,nonce`, tokenHash(r.URL.Query().Get("state")), tokenHash(browser.Value), time.Now().Unix()).Scan(&verifier, &nonce)
	a.cookie(w, a.stateCookie, "", -1)
	if err != nil {
		http.Error(w, "Login expired or already used. Please sign in again.", 400)
		return
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		http.Redirect(w, r, a.origin+"/?login=cancelled", 303)
		return
	}
	data := url.Values{"grant_type": {"authorization_code"}, "code": {r.URL.Query().Get("code")}, "client_id": {a.clientID}, "client_secret": {a.clientSecret}, "redirect_uri": {a.callback}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(r.Context(), "POST", a.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		http.Error(w, "Login unavailable", 503)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := a.client.Do(req)
	if err != nil {
		http.Error(w, "Google sign-in unavailable", 502)
		return
	}
	defer response.Body.Close()
	var tokens struct {
		ID string `json:"id_token"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&tokens) != nil {
		http.Error(w, "Google sign-in failed. Please try again.", 401)
		return
	}
	c := &googleClaims{}
	token, err := jwt.ParseWithClaims(tokens.ID, c, a.keys.key, jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(a.clientID), jwt.WithExpirationRequired())
	if err != nil || !token.Valid || (c.Issuer != "https://accounts.google.com" && c.Issuer != "accounts.google.com") || c.Subject == "" || c.Email == "" || !c.Verified || c.Nonce != nonce || (c.AuthorizedParty != "" && c.AuthorizedParty != a.clientID) {
		http.Error(w, "Google identity could not be verified", 401)
		return
	}
	session, err := randomToken()
	if err != nil {
		http.Error(w, "Login unavailable", 503)
		return
	}
	identity := identityID("https://accounts.google.com", "user", c.Subject)
	// Rotate the current login session rather than carrying it across accounts.
	if old, e := r.Cookie(a.sessionCookie); e == nil {
		_, _ = a.db.Exec(`DELETE FROM login_sessions WHERE hash=?`, tokenHash(old.Value))
	}
	_, err = a.db.Exec(`INSERT INTO login_sessions(hash,account_id,email,expires) VALUES(?,?,?,?)`, tokenHash(session), identity, c.Email, time.Now().Add(8*time.Hour).Unix())
	if err != nil {
		http.Error(w, "Login unavailable", 503)
		return
	}
	if a.onLogin != nil {
		a.onLogin(accountIdentity{ID: identity, Email: c.Email})
	}
	a.cookie(w, a.sessionCookie, session, 8*3600)
	http.Redirect(w, r, a.origin+"/", 303)
}
func (a *googleAuth) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if origin := r.Header.Get("Origin"); origin != "" && origin != a.origin {
			http.Error(w, "Origin denied", 403)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/auth/google":
			a.login(w, r)
			return
		case r.Method == "GET" && r.URL.Path == oauthCallback:
			a.callbackHandler(w, r)
			return
		case r.Method == "POST" && r.URL.Path == "/api/auth/logout":
			// Logout is a same-origin POST and revokes server-side state.
			if r.Header.Get("Origin") != a.origin {
				http.Error(w, "Origin required", 403)
				return
			}
			if c, e := r.Cookie(a.sessionCookie); e == nil {
				_, _ = a.db.Exec(`DELETE FROM login_sessions WHERE hash=?`, tokenHash(c.Value))
			}
			a.cookie(w, a.sessionCookie, "", -1)
			w.WriteHeader(204)
			return
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if r.URL.Path == "/health" && net.ParseIP(host).IsLoopback() {
			next.ServeHTTP(w, r)
			return
		}
		// The existing CI secret authenticates a separate service account. It never
		// receives an owner's store or gateway and is never exposed to the Worker.
		if a.smokeID != "" && a.smokeKey != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("CF-Access-Client-Id")), []byte(a.smokeID)) == 1 && subtle.ConstantTimeCompare([]byte(r.Header.Get("CF-Access-Client-Secret")), []byte(a.smokeKey)) == 1 {
			id := accountIdentity{ID: identityID("386gpt", "service", a.smokeID), Service: true}
			ctx := context.WithValue(r.Context(), identityKey{}, id)
			ctx = context.WithValue(ctx, accessExpiryKey{}, time.Now().Add(time.Hour))
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		c, err := r.Cookie(a.sessionCookie)
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "Sign in with Google"})
			return
		}
		var id accountIdentity
		var expiry int64
		err = a.db.QueryRow(`SELECT account_id,email,expires FROM login_sessions WHERE hash=? AND expires>?`, tokenHash(c.Value), time.Now().Unix()).Scan(&id.ID, &id.Email, &expiry)
		if err != nil {
			a.cookie(w, a.sessionCookie, "", -1)
			writeJSON(w, 401, map[string]string{"error": "Sign in with Google"})
			return
		}
		ctx := context.WithValue(r.Context(), identityKey{}, id)
		ctx = context.WithValue(ctx, accessExpiryKey{}, time.Unix(expiry, 0))
		// WebSockets also check revocation, so logout closes existing connections.
		ctx = context.WithValue(ctx, sessionCheckKey{}, func() bool {
			var n int
			err := a.db.QueryRow(`SELECT COUNT(*) FROM login_sessions WHERE hash=? AND expires>?`, tokenHash(c.Value), time.Now().Unix()).Scan(&n)
			return err == nil && n == 1
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type sessionCheckKey struct{}
