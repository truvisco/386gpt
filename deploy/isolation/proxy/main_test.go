package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"testing"
)

func TestPrivateDestinations(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.68.180", "100.74.13.43", "169.254.169.254", "172.17.0.1", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "64:ff9b::7f00:1", "2002:7f00:1::"} {
		if publicIP(netip.MustParseAddr(ip)) {
			t.Errorf("allowed %s", ip)
		}
	}
}
func TestPublicDestinations(t *testing.T) {
	for _, ip := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(ip)) {
			t.Errorf("denied %s", ip)
		}
	}
}
func TestDialDeniesPrivateAndPorts(t *testing.T) {
	for _, target := range []string{"127.0.0.1:80", "localhost:443", "192.168.68.180:443", "1.1.1.1:22", "[::1]:443"} {
		if c, e := publicDial(context.Background(), "tcp", target); e == nil {
			c.Close()
			t.Errorf("allowed %s", target)
		}
	}
}

func TestFixedModelRoutes(t *testing.T) {
	keyFile := t.TempDir() + "/key"
	if err := os.WriteFile(keyFile, []byte("provider-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROVIDER_KEY_FILE", keyFile)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer provider-secret" {
			t.Error("wrong upstream credential")
		}
		if r.Header.Get("Cookie") != "" {
			t.Error("forwarded cookie")
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	t.Setenv("UPSTREAM", upstream.URL)
	handler := fixed("model")
	for _, path := range []string{"/", "/api/settings", "/v1/models/../admin", "//v1/models"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("allowed %s", path)
		}
	}
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer agent-dummy")
	r.Header.Set("Cookie", "host-secret=value")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestGatewayCredentialSeparation(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/external", []byte("external-key"), 0600)
	os.WriteFile(dir+"/internal", []byte("internal-key"), 0600)
	t.Setenv("GATEWAY_KEY_FILE", dir+"/external")
	t.Setenv("INTERNAL_KEY_FILE", dir+"/internal")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer internal-key" {
			t.Error("did not replace external key")
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	t.Setenv("UPSTREAM", upstream.URL)
	handler := fixed("gateway")
	for _, key := range []string{"", "internal-key", "external-key"} {
		r := httptest.NewRequest("GET", "/v1/capabilities", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		expected := 401
		if key == "external-key" {
			expected = 200
		}
		if w.Code != expected {
			t.Fatalf("unexpected status %d", w.Code)
		}
	}
}
