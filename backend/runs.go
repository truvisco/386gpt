package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type upstreamError struct {
	Status  int
	Message string
}

func (e *upstreamError) Error() string { return e.Message }

func (c *HermesClient) endpoint(runtime string) (*HermesClient, error) {
	if len(c.runtimes) == 0 {
		if runtime == "crash" {
			return c, nil
		}
	} else if endpoint := c.runtimes[runtime]; endpoint != nil {
		return endpoint, nil
	}
	return nil, fmt.Errorf("runtime %s is not configured", runtime)
}
func (c *HermesClient) defaultID() string {
	if c.defaultRuntime == "" {
		return "crash"
	}
	return c.defaultRuntime
}

func (c *HermesClient) json(ctx context.Context, thread, method, path string, body any, key string) (map[string]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := c.newRequest(ctx, thread, method, path, body)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &upstreamError{res.StatusCode, c.redact(hermesError(res).Error())}
	}
	var data map[string]json.RawMessage
	err = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&data)
	return data, err
}

var secretPattern = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]+|(?:gh[pousr]_|github_pat_|sk-)[a-zA-Z0-9_-]{12,}`)

func (c *HermesClient) redact(text string) string {
	if c.apiKey != "" {
		text = strings.ReplaceAll(text, c.apiKey, "[REDACTED]")
	}
	return secretPattern.ReplaceAllString(text, "[REDACTED]")
}
func field(m map[string]json.RawMessage, key string) string {
	var v string
	_ = json.Unmarshal(m[key], &v)
	return v
}

func (s *Server) runtimeClient(thread string) (*HermesClient, Thread, error) {
	t, err := s.store.GetThread(thread)
	if err != nil {
		return nil, t, err
	}
	c, err := s.llm.endpoint(t.RuntimeID)
	return c, t, err
}

func (s *Server) submit(ctx context.Context, thread, id, input string, skills []string) (AgentRun, error) {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{8,100}$`).MatchString(id) {
		return AgentRun{}, errors.New("a valid requestId is required")
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return AgentRun{}, errors.New("message is required")
	}
	// Replays do not depend on discovery or a healthy upstream.
	if prior, err := s.store.run(id); err == nil {
		a, _ := json.Marshal(prior.Skills)
		b, _ := json.Marshal(skills)
		if prior.ThreadID != thread || prior.Input != input || string(a) != string(b) {
			return AgentRun{}, errRunConflict
		}
		s.watch(prior.ID)
		return prior, nil
	}
	c, t, err := s.runtimeClient(thread)
	if err != nil {
		return AgentRun{}, err
	}
	instructions := hermesSystemPrompt
	if len(skills) > 0 {
		catalog, err := c.json(ctx, thread, "GET", "/v1/skills", nil, "")
		if err != nil {
			return AgentRun{}, err
		}
		var entries []struct {
			Name string `json:"name"`
		}
		if err = json.Unmarshal(catalog["data"], &entries); err != nil {
			return AgentRun{}, err
		}
		known := map[string]bool{}
		for _, entry := range entries {
			known[entry.Name] = true
		}
		for _, skill := range skills {
			if !known[skill] {
				return AgentRun{}, fmt.Errorf("unknown or unavailable skill %q", skill)
			}
		}
		names, _ := json.Marshal(skills)
		instructions += "\nThe user selected these skills: " + string(names) + ". Load them with skill_view and follow their instructions for this task."
	}
	session, err := s.store.sessionID(thread)
	if err != nil {
		return AgentRun{}, err
	}
	body, _ := json.Marshal(map[string]any{"input": input, "session_id": session, "instructions": instructions})
	r := AgentRun{ID: id, ThreadID: thread, RuntimeID: t.RuntimeID, SessionID: session, Input: input, Skills: skills, Status: "submitting", CreatedAt: nowUTC(), Request: string(body)}
	r.UpdatedAt = r.CreatedAt
	r, created, err := s.store.reserveRun(r)
	if err != nil {
		return r, err
	}
	if created {
		s.hub.broadcast(thread, socketEvent{Type: "message", Message: &Message{ID: id + "-user", ThreadID: thread, Role: "user", Content: input, CreatedAt: r.CreatedAt}})
		if t.Title == "New conversation" {
			if renamed, e := s.store.RenameThread(thread, titleFromMessage(input)); e == nil {
				s.hub.broadcast(thread, socketEvent{Type: "thread_updated", Thread: &renamed})
			}
		}
	}
	s.watch(r.ID)
	return r, nil
}

func (s *Server) watch(id string) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.workers[id] || s.ctx.Err() != nil {
		return
	}
	s.workers[id] = true
	s.runWG.Add(1)
	go func() {
		defer s.runWG.Done()
		defer func() { s.runMu.Lock(); delete(s.workers, id); s.runMu.Unlock() }()
		s.followRun(id)
	}()
}
func (s *Server) recoverRuns() {
	runs, err := s.store.runs("", true)
	if err != nil {
		slog.Error("recover runs", "error", err)
		return
	}
	for _, run := range runs {
		s.watch(run.ID)
	}
}
func (s *Server) close() { s.cancel(); s.runMu.Lock(); s.runMu.Unlock(); s.runWG.Wait() }

func (s *Server) publishRun(r AgentRun) {
	r.UpdatedAt = nowUTC()
	if err := s.store.saveRun(r); err != nil {
		slog.Error("save run", "run", r.ID, "error", err)
		return
	}
	s.hub.broadcast(r.ThreadID, socketEvent{Type: "run", Run: &r})
}
func (s *Server) event(r AgentRun, kind, key string, data any) {
	e, created, err := s.store.addEvent(r.ThreadID, r.ID, kind, key, data)
	if err != nil {
		slog.Error("save agent event", "error", err)
		return
	}
	if created {
		s.hub.broadcast(r.ThreadID, socketEvent{Type: "agent_event", Event: &e})
	}
}

func (s *Server) followRun(id string) {
	r, err := s.store.run(id)
	if err != nil || !r.active() {
		return
	}
	c, err := s.llm.endpoint(r.RuntimeID)
	if err != nil {
		r.Status = "interrupted"
		r.Error = err.Error()
		s.publishRun(r)
		return
	}
	delay := time.Second
	for r.UpstreamID == "" && s.ctx.Err() == nil {
		created, parseErr := time.Parse(time.RFC3339Nano, r.CreatedAt)
		if parseErr != nil || time.Since(created) >= 23*time.Hour {
			r.Status = "interrupted"
			r.Error = "Acceptance could not be confirmed within the safe retry window. Inspect execution history before retrying."
			s.publishRun(r)
			return
		}
		// Retrying the exact saved body/key resolves lost acceptance without executing twice.
		data, err := c.json(s.ctx, r.ThreadID, "POST", "/v1/runs", json.RawMessage(r.Request), r.ID)
		if err == nil {
			r.UpstreamID = field(data, "run_id")
			if r.UpstreamID == "" {
				err = errors.New("Hermes acceptance omitted run_id")
			}
		}
		if err != nil {
			var ue *upstreamError
			if errors.As(err, &ue) && ue.Status >= 400 && ue.Status < 500 && ue.Status != 429 {
				r.Status = "failed"
				r.Error = err.Error()
				s.publishRun(r)
				return
			}
			r.Error = "Waiting for Hermes: " + c.redact(err.Error())
			s.publishRun(r)
			if !pause(s.ctx, delay) {
				return
			}
			if delay < 15*time.Second {
				delay *= 2
			}
			continue
		}
		r.Status = "queued"
		r.Error = ""
		s.publishRun(r)
	}
	if s.ctx.Err() != nil {
		return
	}
	events := make(chan map[string]json.RawMessage, 64)
	streamCtx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	go func() {
		defer close(events)
		_ = c.runEvents(streamCtx, r.ThreadID, r.UpstreamID, func(data map[string]json.RawMessage) error {
			select {
			case events <- data:
				return nil
			case <-streamCtx.Done():
				return streamCtx.Err()
			}
		})
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastTranscript := time.Time{}
	stopSent := false
	for {
		select {
		case <-s.ctx.Done():
			return
		case data, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			kind := field(data, "event")
			if kind == "reasoning.available" {
				continue
			} // Deliberation is not execution evidence.
			raw, _ := json.Marshal(data)
			var safe map[string]json.RawMessage
			_ = json.Unmarshal([]byte(c.redact(string(raw))), &safe)
			s.event(r, kind, "", safe)
			if kind == "message.delta" {
				r.Output += c.redact(field(data, "delta"))
				s.hub.broadcast(r.ThreadID, socketEvent{Type: "message", Message: &Message{ID: r.ID + "-assistant", ThreadID: r.ThreadID, Role: "assistant", Content: r.Output, CreatedAt: r.CreatedAt, Streaming: true}})
			}
			if kind == "approval.request" {
				r.Status = "waiting_for_approval"
				r.Approval, _ = json.Marshal(safe)
				s.publishRun(r)
			}
			if strings.HasPrefix(kind, "tool.") {
				s.hub.broadcast(r.ThreadID, socketEvent{Type: "agent_activity", Activity: &HermesActivity{State: strings.TrimPrefix(kind, "tool."), Tool: field(data, "tool"), Detail: field(data, "preview")}})
			}
			if !strings.HasPrefix(kind, "run.") {
				continue
			}
		case <-ticker.C:
		}
		if !stopSent && s.store.stopRequested(r.ID) {
			_, stopErr := c.json(s.ctx, r.ThreadID, "POST", "/v1/runs/"+url.PathEscape(r.UpstreamID)+"/stop", map[string]string{}, "")
			if stopErr == nil {
				stopSent = true
				s.event(r, "control.stop", "stopped:"+r.ID, map[string]string{"status": "delivered"})
			}
		}
		status, err := c.json(s.ctx, r.ThreadID, "GET", "/v1/runs/"+url.PathEscape(r.UpstreamID), nil, "")
		if err != nil {
			var ue *upstreamError
			if errors.As(err, &ue) && ue.Status == 404 {
				r.Status = "interrupted"
				r.Error = "Hermes no longer has this run. It has not been resubmitted."
				s.publishRun(r)
				return
			}
			r.Error = "Hermes connection interrupted; reconnecting"
			s.publishRun(r)
			continue
		}
		nextStatus := field(status, "status")
		switch nextStatus {
		case "queued", "running", "waiting_for_approval", "stopping", "completed", "failed", "cancelled", "interrupted":
		default:
			r.Error = "Hermes returned an unknown run status; reconnecting"
			s.publishRun(r)
			continue
		}
		r.Error = c.redact(field(status, "error"))
		r.Status = nextStatus
		if session := field(status, "session_id"); session != "" {
			r.SessionID = session
		}
		r.Approval = status["approval"]
		r.PendingSteer = status["pending_steer"]
		if output := field(status, "output"); output != "" {
			r.Output = c.redact(output)
		}
		if time.Since(lastTranscript) > 5*time.Second || !r.active() {
			if err := s.syncTranscript(s.ctx, c, &r); err != nil {
				s.event(r, "transcript.unavailable", "", map[string]string{"error": c.redact(err.Error())})
			}
			lastTranscript = time.Now()
		}
		s.publishRun(r)
		if !r.active() {
			if r.Output != "" {
				s.hub.broadcast(r.ThreadID, socketEvent{Type: "message", Message: &Message{ID: r.ID + "-assistant", ThreadID: r.ThreadID, Role: "assistant", Content: r.Output, CreatedAt: nowUTC()}})
			}
			if r.Status == "failed" || r.Status == "interrupted" {
				s.hub.broadcast(r.ThreadID, socketEvent{Type: "error", Error: r.Error})
			}
			return
		}
	}
}

func pause(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (c *HermesClient) runEvents(ctx context.Context, thread, id string, accept func(map[string]json.RawMessage) error) error {
	req, err := c.newRequest(ctx, thread, "GET", "/v1/runs/"+url.PathEscape(id)+"/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return hermesError(res)
	}
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 65536), 4<<20)
	var parts []string
	name := ""
	emit := func() error {
		if len(parts) == 0 {
			return nil
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal([]byte(strings.Join(parts, "\n")), &data); err != nil {
			return err
		}
		if field(data, "event") == "" {
			data["event"], _ = json.Marshal(name)
		}
		parts = nil
		return accept(data)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := emit(); err != nil {
				return err
			}
		} else if strings.HasPrefix(line, "data:") {
			parts = append(parts, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		} else if strings.HasPrefix(line, "event:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return emit()
}

func (s *Server) syncTranscript(ctx context.Context, c *HermesClient, r *AgentRun) error {
	for offset := 0; ; offset += 500 {
		page, err := c.json(ctx, r.ThreadID, "GET", fmt.Sprintf("/api/sessions/%s/messages?limit=500&order=oldest&offset=%d", url.PathEscape(r.SessionID), offset), nil, "")
		if err != nil {
			return err
		}
		if id := field(page, "session_id"); id != "" {
			r.SessionID = id
		}
		var rows []map[string]json.RawMessage
		if err = json.Unmarshal(page["data"], &rows); err != nil {
			return err
		}
		for _, row := range rows {
			role := field(row, "role")
			if role == "assistant" && len(row["tool_calls"]) > 0 && string(row["tool_calls"]) != "null" {
				callsRaw := row["tool_calls"]
				if len(callsRaw) > 0 && callsRaw[0] == '"' {
					callsRaw = []byte(field(row, "tool_calls"))
				}
				var calls []map[string]json.RawMessage
				if json.Unmarshal(callsRaw, &calls) != nil {
					continue
				}
				for _, call := range calls {
					var fn map[string]json.RawMessage
					_ = json.Unmarshal(call["function"], &fn)
					id := field(call, "id")
					if id == "" {
						continue
					}
					s.event(*r, "tool.call", "call:"+id, map[string]any{"callId": id, "tool": field(fn, "name"), "arguments": c.redact(field(fn, "arguments")), "timestamp": row["timestamp"]})
				}
			}
			if role == "tool" {
				id := field(row, "tool_call_id")
				if id == "" {
					id = string(row["id"])
				}
				s.event(*r, "tool.result", "result:"+id, map[string]any{"callId": id, "tool": field(row, "tool_name"), "result": c.redact(field(row, "content")), "timestamp": row["timestamp"]})
			}
		}
		if len(rows) < 500 {
			return nil
		}
	}
}
