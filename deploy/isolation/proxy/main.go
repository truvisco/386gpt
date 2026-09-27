// Boundary proxies: no control API, no arbitrary private upstreams, no agent secrets.
package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

var reserved = []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "::/96", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2002::/16", "fc00::/7", "fe80::/10", "ff00::/8"}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, s := range reserved {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	return true
}
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || (port != "80" && port != "443") {
		return nil, fmt.Errorf("destination port denied")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("resolution failed")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, fmt.Errorf("destination denied")
		}
	}
	// Dial the validated IP itself: do not re-resolve after checking (DNS rebinding).
	var last error
	for _, ip := range ips {
		c, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return c, nil
		}
		last = e
	}
	return nil, last
}
func egress() http.Handler {
	transport := &http.Transport{DialContext: publicDial, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second}
	forward := &httputil.ReverseProxy{Director: func(r *http.Request) { r.Header.Del("Proxy-Authorization"); r.Header.Del("Proxy-Connection") }, Transport: transport, ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "Destination unavailable or denied", http.StatusForbidden)
	}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			if r.URL.Scheme != "http" || r.URL.Host == "" || r.URL.User != nil {
				http.Error(w, "HTTP proxy request required", 400)
				return
			}
			forward.ServeHTTP(w, r)
			return
		}
		if _, p, e := net.SplitHostPort(r.Host); e != nil || p != "443" {
			http.Error(w, "CONNECT requires port 443", 403)
			return
		}
		upstream, err := publicDial(r.Context(), "tcp", r.Host)
		if err != nil {
			http.Error(w, "Destination denied", 403)
			return
		}
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		defer client.Close()
		defer upstream.Close()
		deadline := time.Now().Add(30 * time.Minute)
		client.SetDeadline(deadline)
		upstream.SetDeadline(deadline)
		buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buffer.Flush()
		go func() { io.Copy(upstream, buffer); upstream.Close() }()
		io.Copy(client, upstream)
	})
}
func secret(name string) string {
	b, e := os.ReadFile(os.Getenv(name))
	if e != nil {
		log.Fatal("Missing secret file: ", name)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		log.Fatal("Empty secret: ", name)
	}
	return s
}
func fixed(mode string) http.Handler {
	upstream, e := url.Parse(os.Getenv("UPSTREAM"))
	if e != nil || upstream.Host == "" || upstream.Scheme != "http" {
		log.Fatal("Invalid fixed upstream")
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.Transport = &http.Transport{Proxy: nil, ResponseHeaderTimeout: 5 * time.Minute}
	proxy.FlushInterval = -1
	internal := ""
	if mode == "gateway" {
		internal = secret("INTERNAL_KEY_FILE")
	}
	old := proxy.Director
	key := ""
	if mode == "model" {
		key = secret("PROVIDER_KEY_FILE")
	} else {
		key = secret("GATEWAY_KEY_FILE")
	}
	proxy.Director = func(r *http.Request) {
		old(r)
		r.Host = upstream.Host
		r.Header.Del("Cookie")
		r.Header.Del("Cf-Access-Jwt-Assertion")
		token := key
		if internal != "" {
			token = internal
		}
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "model" {
			if !((r.Method == "GET" && r.URL.Path == "/v1/models") || (r.Method == "POST" && (r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/responses"))) {
				http.Error(w, "Inference endpoint only", 403)
				return
			}
		} else if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
			http.Error(w, "Unauthorized", 401)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
		proxy.ServeHTTP(w, r)
	})
}
func main() {
	mode := os.Getenv("MODE")
	var h http.Handler
	switch mode {
	case "egress":
		h = egress()
	case "model", "gateway":
		h = fixed(mode)
	default:
		log.Fatal("Invalid MODE")
	}
	s := &http.Server{Addr: ":8080", Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768}
	log.Fatal(s.ListenAndServe())
}
