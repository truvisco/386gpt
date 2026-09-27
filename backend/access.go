package main

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type accessClaims struct {
	jwt.RegisteredClaims
	Email      string `json:"email"`
	CommonName string `json:"common_name"`
	Type       string `json:"type"`
}
type accessVerifier struct {
	issuer, audience, email, service string
	allowedEmails                    string
	allowAll                         bool
	mu                               sync.Mutex
	keys                             map[string]*rsa.PublicKey
	fetched                          time.Time
	client                           *http.Client
	jwksURL                          string
}

func newAccessVerifier(issuer, audience, email, service string) (*accessVerifier, error) {
	u, e := url.Parse(issuer)
	if e != nil || u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".cloudflareaccess.com") || u.Port() != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || audience == "" || email == "" {
		return nil, errors.New("valid Access issuer, audience and owner email are required")
	}
	return &accessVerifier{issuer: issuer, audience: audience, email: email, service: service, client: &http.Client{Timeout: 10 * time.Second}}, nil
}
func (a *accessVerifier) key(t *jwt.Token) (any, error) {
	kid, ok := t.Header["kid"].(string)
	if !ok || kid == "" {
		return nil, errors.New("missing key ID")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Since(a.fetched) < 5*time.Minute {
		if key := a.keys[kid]; key != nil {
			return key, nil
		}
		return nil, errors.New("unknown key")
	}
	endpoint := a.jwksURL
	if endpoint == "" {
		endpoint = a.issuer + "/cdn-cgi/access/certs"
	}
	response, e := a.client.Get(endpoint)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("Access keys unavailable")
	}
	var document struct {
		Keys []struct{ Kid, Kty, Alg, N, E string }
	}
	if e = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); e != nil {
		return nil, e
	}
	keys := map[string]*rsa.PublicKey{}
	for _, j := range document.Keys {
		if j.Kty != "RSA" || (j.Alg != "" && j.Alg != "RS256") {
			continue
		}
		n, ne := base64.RawURLEncoding.DecodeString(j.N)
		b, ee := base64.RawURLEncoding.DecodeString(j.E)
		if ne != nil || ee != nil || len(b) > 4 || len(n) < 256 {
			continue
		}
		exponent := new(big.Int).SetBytes(b).Int64()
		if exponent < 3 || exponent > 2147483647 {
			continue
		}
		keys[j.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent)}
	}
	if len(keys) == 0 {
		return nil, errors.New("empty key set")
	}
	a.keys = keys
	a.fetched = time.Now()
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, errors.New("unknown key")
}
func (a *accessVerifier) verify(raw string) error {
	if raw == "" || len(raw) > 32768 {
		return errors.New("missing or oversized token")
	}
	c := &accessClaims{}
	t, e := jwt.ParseWithClaims(raw, c, a.key, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(a.issuer), jwt.WithAudience(a.audience), jwt.WithExpirationRequired(), jwt.WithLeeway(15*time.Second))
	if e != nil || !t.Valid {
		return errors.New("invalid token")
	}
	if c.Subject != "" && c.Email != "" && c.Type == "app" && a.emailAllowed(c.Email) {
		return nil
	}
	if a.service != "" && c.CommonName == a.service && c.Type == "app" {
		return nil
	}
	return errors.New("identity denied")
}

type identityKey struct{}
type accountIdentity struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Service bool   `json:"service"`
}

func identityID(issuer, kind, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + kind + "\x00" + subject))
	return hex.EncodeToString(sum[:])
}
func (a *accessVerifier) emailAllowed(email string) bool {
	if a.allowAll {
		return true
	}
	allowed := a.allowedEmails
	if allowed == "" {
		allowed = a.email
	}
	for _, entry := range strings.Split(allowed, ",") {
		if strings.EqualFold(strings.TrimSpace(entry), email) {
			return true
		}
	}
	return false
}
func (a *accessVerifier) identity(c accessClaims) accountIdentity {
	if c.CommonName == a.service && a.service != "" {
		return accountIdentity{ID: identityID(a.issuer, "service", c.CommonName), Service: true}
	}
	return accountIdentity{ID: identityID(a.issuer, "user", c.Subject), Email: c.Email}
}

type accessExpiryKey struct{}

func (a *accessVerifier) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health contains no state and is available only to the loopback installer.
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if r.URL.Path == "/health" && net.ParseIP(host).IsLoopback() {
			next.ServeHTTP(w, r)
			return
		}
		if e := a.verify(r.Header.Get("Cf-Access-Jwt-Assertion")); e != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Owner login required"})
			return
		}
		var claims accessClaims
		// Signature and claims were validated above; carry expiry to upgraded sockets.
		_, _, _ = jwt.NewParser().ParseUnverified(r.Header.Get("Cf-Access-Jwt-Assertion"), &claims)
		ctx := context.WithValue(r.Context(), accessExpiryKey{}, claims.ExpiresAt.Time)
		ctx = context.WithValue(ctx, identityKey{}, a.identity(claims))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func authentication(address string, next http.Handler) (http.Handler, error) {
	if os.Getenv("AUTH_MODE") == "development" {
		host, _, e := net.SplitHostPort(address)
		if e != nil || !net.ParseIP(host).IsLoopback() {
			return nil, errors.New("development authentication requires a loopback listen address")
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := accountIdentity{ID: identityID("local", "development", "owner"), Email: envOr("ACCESS_OWNER_EMAIL", "mauriciootta@gmail.com")}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, identity)))
		}), nil
	}
	a, e := newAccessVerifier(os.Getenv("ACCESS_ISSUER"), os.Getenv("ACCESS_AUDIENCE"), os.Getenv("ACCESS_OWNER_EMAIL"), os.Getenv("ACCESS_SERVICE_CLIENT_ID"))
	if e != nil {
		return nil, fmt.Errorf("authentication: %w", e)
	}
	a.allowedEmails = os.Getenv("ACCESS_ALLOWED_EMAILS")
	a.allowAll = os.Getenv("ACCESS_ALLOW_ALL") == "true"
	return a.protect(next), nil
}
func allowedOrigin(origin string) bool {
	if origin == "" {
		return true
	} // Non-browser callers still require authentication.
	if origin == "https://386gpt.truvis.co" {
		return true
	}
	if os.Getenv("AUTH_MODE") == "development" {
		return origin == "http://localhost:5173" || origin == "http://127.0.0.1:5173" || origin == "http://127.0.0.1:15173"
	}
	return false
}
