package main

import (
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

type agentConnection struct {
	BaseURL    string `yaml:"base_url"`
	APIKey     string `yaml:"api_key"`
	SessionKey string `yaml:"session_key"`
}

type tenantConnection struct {
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
}

type hermesConfig struct {
	Tenants        tenantConnection           `yaml:"tenants"`
	Agent          agentConnection            `yaml:"agent"`
	DefaultRuntime string                     `yaml:"default_runtime"`
	Runtimes       map[string]agentConnection `yaml:"runtimes"`
}

type HermesActivity struct {
	State  string `json:"state"`
	Tool   string `json:"tool,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type HermesClient struct {
	defaultRuntime string
	runtimes       map[string]*HermesClient
	baseURL        string
	apiKey         string
	sessionKey     string
	httpClient     *http.Client
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
	if len(config.Runtimes) > 0 {
		root := &HermesClient{defaultRuntime: config.DefaultRuntime, runtimes: map[string]*HermesClient{}}
		for name, entry := range config.Runtimes {
			if name != "local" && name != "crash" {
				return nil, fmt.Errorf("unknown runtime %q", name)
			}
			if entry.BaseURL == "" || entry.APIKey == "" || entry.SessionKey == "" {
				return nil, fmt.Errorf("runtime %s requires base_url, api_key and session_key", name)
			}
			root.runtimes[name] = &HermesClient{baseURL: strings.TrimRight(entry.BaseURL, "/"), apiKey: entry.APIKey, sessionKey: entry.SessionKey, httpClient: &http.Client{}}
		}
		if root.runtimes[root.defaultRuntime] == nil || root.runtimes["crash"] == nil {
			return nil, errors.New("default runtime and legacy crash runtime must be configured")
		}
		root.baseURL = root.runtimes[root.defaultRuntime].baseURL
		return root, nil
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
		defaultRuntime: "crash",
		baseURL:        strings.TrimRight(config.Agent.BaseURL, "/"),
		apiKey:         config.Agent.APIKey,
		sessionKey:     config.Agent.SessionKey,
		httpClient:     &http.Client{},
	}, nil
}

func hermesSessionID(threadID string) string { return "386gpt-" + threadID }

func (c *HermesClient) newRequest(ctx context.Context, threadID, method, path string, body any) (*http.Request, error) {
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
	// Hermes uses this key for terminal state as well as memory scope. Sharing
	// it across threads leaks cwd/environment changes into unrelated chats.
	request.Header.Set("X-Hermes-Session-Key", c.sessionKey+":thread:"+threadID)
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
