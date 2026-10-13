package main

import (
	"encoding/json"
	"net/http"
)

// GitHub credentials travel directly to the account's gateway; never persist them
// in conversation/run records or return them in API responses.
func (s *Server) githubSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	c, runtime, err := s.selectedRuntime(r)
	if err != nil {
		writeError(w, err)
		return
	}
	path := "/v1/github"
	var body any
	if r.Method == http.MethodPost {
		switch r.PathValue("action") {
		case "device", "disconnect":
			body = map[string]string{}
		case "token":
			var input struct {
				Token string `json:"token"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Token) < 10 || len(input.Token) > 4096 {
				writeJSON(w, 400, map[string]string{"error": "Enter a valid GitHub token."})
				return
			}
			body = input
		default:
			http.NotFound(w, r)
			return
		}
		path += "/" + r.PathValue("action")
	}
	data, err := c.json(r.Context(), r.URL.Query().Get("thread_id"), r.Method, path, body, "")
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "GitHub settings unavailable or sign-in failed. Check token permissions and that the workspace image is up to date."})
		return
	}
	// Allowlist the response so an upstream CLI error can never echo a token.
	result := map[string]string{"runtime": runtime}
	for _, name := range []string{"state", "login", "user_code", "verification_uri", "error"} {
		if value := field(data, name); value != "" {
			result[name] = value
		}
	}
	writeJSON(w, 200, result)
}
