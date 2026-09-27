package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (s *Server) selectedRuntime(r *http.Request) (*HermesClient, string, error) {
	name := s.llm.defaultID()
	if thread := r.URL.Query().Get("thread_id"); thread != "" {
		t, err := s.store.GetThread(thread)
		if err != nil {
			return nil, "", err
		}
		name = t.RuntimeID
	}
	c, err := s.llm.endpoint(name)
	return c, name, err
}
func (s *Server) runtime(w http.ResponseWriter, r *http.Request) {
	c, name, err := s.selectedRuntime(r)
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := c.json(r.Context(), r.URL.Query().Get("thread_id"), "GET", "/v1/capabilities", nil, "")
	if err != nil {
		writeJSON(w, 200, map[string]any{"id": name, "provider": "Hermes", "model": "Unavailable", "healthy": false, "error": err.Error()})
		return
	}
	var runtime map[string]any
	_ = json.Unmarshal(data["runtime"], &runtime)
	if runtime == nil {
		runtime = map[string]any{}
	}
	runtime["id"] = name
	runtime["healthy"] = true
	runtime["capabilities"] = data["features"]
	if runtime["model"] == nil {
		runtime["model"] = field(data, "model")
	}
	if runtime["provider"] == nil {
		runtime["provider"] = "Hermes"
	}
	writeJSON(w, 200, runtime)
}
func (s *Server) skills(w http.ResponseWriter, r *http.Request) {
	c, _, err := s.selectedRuntime(r)
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := c.json(r.Context(), r.URL.Query().Get("thread_id"), "GET", "/v1/skills", nil, "")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, data)
}
func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID string   `json:"requestId"`
		Message   string   `json:"message"`
		Skills    []string `json:"skills"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	run, err := s.submit(r.Context(), r.PathValue("id"), body.RequestID, body.Message, body.Skills)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"run": run})
}
func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.GetThread(r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	runs, err := s.store.runs(r.PathValue("id"), false)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}
func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	c, t, err := s.runtimeClient(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if after < 0 {
		after = 0
	}
	// Import old conversations' evidence lazily; no commands are executed.
	if after == 0 && r.URL.Query().Get("sync") == "1" {
		session, e := s.store.sessionID(t.ID)
		if e == nil {
			run := AgentRun{ThreadID: t.ID, RuntimeID: t.RuntimeID, SessionID: session}
			_ = s.syncTranscript(r.Context(), c, &run)
		}
	}
	events, err := s.store.events(t.ID, after)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"events": events, "hasMore": len(events) == 200})
}
func (s *Server) controlRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.run(r.PathValue("run"))
	if err != nil {
		writeError(w, err)
		return
	}
	if run.ThreadID != r.PathValue("id") {
		writeError(w, errNotFound)
		return
	}
	if !run.active() {
		writeJSON(w, 409, map[string]string{"error": "Run is not ready for this control"})
		return
	}
	var body map[string]json.RawMessage
	if !decodeJSON(w, r, &body) {
		return
	}
	action := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if action == "stop" {
		// Persist intent even if the acceptance response was lost. The worker must
		// resolve that acceptance before it can safely stop the upstream run.
		_, _, err := s.store.addEvent(run.ThreadID, run.ID, "control.stop.requested", "stop:"+run.ID, map[string]string{"status": "requested"})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 202, map[string]string{"status": "stop_requested"})
		return
	}
	if run.UpstreamID == "" {
		writeJSON(w, 409, map[string]string{"error": "Run is awaiting Hermes acceptance"})
		return
	}
	switch action {
	case "approval":
		var pending map[string]json.RawMessage
		_ = json.Unmarshal(run.Approval, &pending)
		requestID, choice := field(body, "request_id"), field(body, "choice")
		var choices []string
		_ = json.Unmarshal(pending["choices"], &choices)
		allowed := false
		for _, v := range choices {
			if v == choice {
				allowed = true
			}
		}
		if requestID == "" || requestID != field(pending, "request_id") || !allowed {
			writeJSON(w, 409, map[string]string{"error": "Approval is stale or this choice is unavailable"})
			return
		}
		body = map[string]json.RawMessage{"request_id": body["request_id"], "choice": body["choice"]}
	case "steer":
		if strings.TrimSpace(field(body, "input")) == "" {
			writeError(w, errors.New("input is required"))
			return
		}
		body = map[string]json.RawMessage{"input": body["input"]}
	case "stop":
		body = map[string]json.RawMessage{}
	}
	c, err := s.llm.endpoint(run.RuntimeID)
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := c.json(r.Context(), run.ThreadID, "POST", "/v1/runs/"+url.PathEscape(run.UpstreamID)+"/"+action, body, "")
	if err != nil {
		var ue *upstreamError
		if errors.As(err, &ue) {
			writeJSON(w, ue.Status, map[string]string{"error": err.Error()})
		} else {
			writeError(w, err)
		}
		return
	}
	s.event(run, "control."+action, "", body)
	writeJSON(w, 200, data)
}
