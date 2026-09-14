package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const hermesSystemPrompt = "You are chatting with the user through 386GPT, a persistent personal chat surface. Behave as you would in a direct Telegram conversation: be conversational, use Hermes tools, skills, and memory when useful, and return a clear final response."

type hermesConfig struct {
	Agent struct {
		BaseURL    string `yaml:"base_url"`
		APIKey     string `yaml:"api_key"`
		SessionKey string `yaml:"session_key"`
	} `yaml:"agent"`
}

type HermesActivity struct {
	State  string `json:"state"`
	Tool   string `json:"tool,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type HermesClient struct {
	baseURL    string
	apiKey     string
	sessionKey string
	httpClient *http.Client
}

type hermesSessionResponse struct {
	Session struct {
		ID string `json:"id"`
	} `json:"session"`
}

type hermesErrorResponse struct {
	Error any `json:"error"`
}

func defaultHermesConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hermes", "386gpt.yaml"), nil
}

func loadHermesAgent(path string) (*HermesClient, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Hermes agent config: %w", err)
	}
	var config hermesConfig
	if err := yaml.Unmarshal(contents, &config); err != nil {
		return nil, fmt.Errorf("parse Hermes agent config: %w", err)
	}
	if strings.TrimSpace(config.Agent.BaseURL) == "" {
		return nil, errors.New("Hermes agent config must define agent.base_url")
	}
	if strings.TrimSpace(config.Agent.APIKey) == "" {
		return nil, errors.New("Hermes agent config must define agent.api_key")
	}
	if strings.TrimSpace(config.Agent.SessionKey) == "" {
		return nil, errors.New("Hermes agent config must define agent.session_key")
	}
	return &HermesClient{
		baseURL:    strings.TrimRight(config.Agent.BaseURL, "/"),
		apiKey:     config.Agent.APIKey,
		sessionKey: config.Agent.SessionKey,
		httpClient: &http.Client{},
	}, nil
}

func hermesSessionID(threadID string) string { return "386gpt-" + threadID }

func (c *HermesClient) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("X-Hermes-Session-Key", c.sessionKey)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func hermesError(response *http.Response) error {
	detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	var payload hermesErrorResponse
	if json.Unmarshal(detail, &payload) == nil && payload.Error != nil {
		switch value := payload.Error.(type) {
		case string:
			return fmt.Errorf("Hermes Agent returned %s: %s", response.Status, value)
		case map[string]any:
			if message, ok := value["message"].(string); ok {
				return fmt.Errorf("Hermes Agent returned %s: %s", response.Status, message)
			}
		}
	}
	return fmt.Errorf("Hermes Agent returned %s: %s", response.Status, strings.TrimSpace(string(detail)))
}

func (c *HermesClient) ensureSession(ctx context.Context, threadID string) error {
	request, err := c.newRequest(ctx, http.MethodPost, "/api/sessions", map[string]any{
		"id":            hermesSessionID(threadID),
		"source":        "api_server",
		"system_prompt": hermesSystemPrompt,
	})
	if err != nil {
		return err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("contact Hermes Agent: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return nil
	}
	if response.StatusCode != http.StatusCreated {
		return hermesError(response)
	}
	var created hermesSessionResponse
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		return fmt.Errorf("decode Hermes session: %w", err)
	}
	if created.Session.ID == "" {
		return errors.New("Hermes Agent created a session without an ID")
	}
	return nil
}

func (c *HermesClient) Stream(ctx context.Context, threadID, input string, onChunk func(string), onActivity func(HermesActivity)) (string, error) {
	if err := c.ensureSession(ctx, threadID); err != nil {
		return "", err
	}
	request, err := c.newRequest(ctx, http.MethodPost, "/api/sessions/"+hermesSessionID(threadID)+"/chat/stream", map[string]string{
		"message": input,
	})
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("contact Hermes Agent: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", hermesError(response)
	}

	var assembled, completed strings.Builder
	eventName := "message"
	dataLines := make([]string, 0, 1)
	processEvent := func() error {
		if len(dataLines) == 0 {
			eventName = "message"
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		var payload struct {
			Delta    string `json:"delta"`
			Content  string `json:"content"`
			Message  any    `json:"message"`
			ToolName string `json:"tool_name"`
			Preview  string `json:"preview"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return fmt.Errorf("decode Hermes %s event: %w", eventName, err)
		}
		switch eventName {
		case "assistant.delta":
			if payload.Delta != "" {
				assembled.WriteString(payload.Delta)
				onChunk(payload.Delta)
			}
		case "assistant.completed":
			completed.Reset()
			completed.WriteString(payload.Content)
		case "tool.started", "tool.completed", "tool.failed", "tool.progress":
			onActivity(HermesActivity{State: strings.TrimPrefix(eventName, "tool."), Tool: payload.ToolName, Detail: payload.Preview})
		case "error":
			message, _ := payload.Message.(string)
			if message == "" {
				message = "Hermes Agent run failed"
			}
			return errors.New(message)
		}
		eventName = "message"
		return nil
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := processEvent(); err != nil {
				return assembled.String(), err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return assembled.String(), fmt.Errorf("read Hermes Agent stream: %w", err)
	}
	if err := processEvent(); err != nil {
		return assembled.String(), err
	}
	final := completed.String()
	if final == "" {
		final = assembled.String()
	}
	if final == "" {
		return "", errors.New("Hermes Agent returned an empty response")
	}
	if suffix := strings.TrimPrefix(final, assembled.String()); suffix != final && suffix != "" {
		onChunk(suffix)
	}
	return final, nil
}
