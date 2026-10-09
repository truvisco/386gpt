package main

import (
	"crypto/rand"
	"crypto/rsa"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDevelopmentOrigins(t *testing.T) {
	t.Setenv("AUTH_MODE", "development")
	for _, origin := range []string{"https://386gpt.dev14.portnumber53.com", "http://localhost:21350", "http://127.0.0.1:21352"} {
		if !allowedOrigin(origin) {
			t.Errorf("development origin rejected: %s", origin)
		}
	}
	for _, origin := range []string{"https://other.dev14.portnumber53.com", "https://386gpt.dev14.portnumber53.com.evil.example", "http://386gpt.dev14.portnumber53.com"} {
		if allowedOrigin(origin) {
			t.Errorf("untrusted origin accepted: %s", origin)
		}
	}
	t.Setenv("AUTH_MODE", "production")
	if allowedOrigin("https://386gpt.dev14.portnumber53.com") {
		t.Fatal("development origin accepted in production")
	}
}

func TestAccessAuthentication(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	a := &accessVerifier{issuer: "https://test.cloudflareaccess.com", audience: "app", email: "mauriciootta@gmail.com", service: "ci.access", keys: map[string]*rsa.PublicKey{"test": &key.PublicKey}, fetched: time.Now()}
	base := accessClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "owner-subject", Issuer: a.issuer, Audience: jwt.ClaimStrings{"app"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, Email: a.email, Type: "app"}
	cases := []struct {
		name  string
		edit  func(*accessClaims)
		valid bool
	}{
		{"owner", func(c *accessClaims) {}, true},
		{"other email", func(c *accessClaims) { c.Email = "other@example.com" }, false},
		{"expired", func(c *accessClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour)) }, false},
		{"no expiry", func(c *accessClaims) { c.ExpiresAt = nil }, false},
		{"wrong audience", func(c *accessClaims) { c.Audience = jwt.ClaimStrings{"other"} }, false},
		{"wrong issuer", func(c *accessClaims) { c.Issuer = "https://other.cloudflareaccess.com" }, false},
		{"service", func(c *accessClaims) { c.Email = ""; c.CommonName = a.service }, true},
		{"wrong service", func(c *accessClaims) { c.Email = ""; c.CommonName = "other" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.edit(&c)
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
			token.Header["kid"] = "test"
			raw, _ := token.SignedString(key)
			if (a.verify(raw) == nil) != tc.valid {
				t.Fatal("unexpected authorization")
			}
		})
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, base)
	token.Header["kid"] = "test"
	raw, _ := token.SignedString([]byte("fake"))
	if a.verify(raw) == nil {
		t.Fatal("algorithm confusion")
	}
	next := a.protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, path := range []string{"/api/threads", "/ws", "/health"} {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("Cf-Access-Authenticated-User-Email", a.email)
		w := httptest.NewRecorder()
		next.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("unauthenticated %s allowed", path)
		}
	}
}
func TestDevelopmentCannotBindPublicly(t *testing.T) {
	t.Setenv("AUTH_MODE", "development")
	for _, address := range []string{":21351", "0.0.0.0:21351", "192.168.1.1:21351"} {
		if _, e := authentication(address, http.NotFoundHandler()); e == nil {
			t.Fatal("public bypass", address)
		}
	}
	if _, e := authentication("127.0.0.1:21351", http.NotFoundHandler()); e != nil {
		t.Fatal(e)
	}
}
func TestAuthFailsClosed(t *testing.T) {
	t.Setenv("AUTH_MODE", "")
	t.Setenv("ACCESS_ISSUER", "")
	if _, e := authentication("127.0.0.1:21351", http.NotFoundHandler()); e == nil {
		t.Fatal("missing config accepted")
	}
}
