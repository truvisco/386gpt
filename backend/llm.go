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

type hermesConfig struct {
	Model struct {
		Default  string `yaml:"default"`
		Provider string `yaml:"provider"`
	} `yaml:"model"`
	CustomProviders []struct {
		Name    string `yaml:"name"`
		BaseURL string `yaml:"base_url"`
		APIKey  string `yaml:"api_key"`
		APIMode string `yaml:"api_mode"`
	} `yaml:"custom_providers"`
}

type LLMClient struct {
	provider   string
	model      string
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type completionRequest struct {
	Model    string       `json:"model"`
	Messages []llmMessage `json:"messages"`
	Stream   bool         `json:"stream"`
}

type llmMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionResponse struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func defaultHermesConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hermes", "config.yaml"), nil
}

func loadHermesLLM(path string) (*LLMClient, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Hermes config: %w", err)
	}
	var config hermesConfig
	if err := yaml.Unmarshal(contents, &config); err != nil {
		return nil, fmt.Errorf("parse Hermes config: %w", err)
	}
	if config.Model.Provider == "" || config.Model.Default == "" {
		return nil, errors.New("Hermes config must define model.provider and model.default")
	}
	for _, provider := range config.CustomProviders {
		if provider.Name != config.Model.Provider {
			continue
		}
		if provider.APIMode != "" && provider.APIMode != "chat_completions" {
			return nil, fmt.Errorf("Hermes provider %q uses unsupported api_mode %q", provider.Name, provider.APIMode)
		}
		if provider.BaseURL == "" {
			return nil, fmt.Errorf("Hermes provider %q has no base_url", provider.Name)
		}
		return &LLMClient{
			provider:   provider.Name,
			model:      config.Model.Default,
			baseURL:    strings.TrimRight(provider.BaseURL, "/"),
			apiKey:     provider.APIKey,
			httpClient: &http.Client{},
		}, nil
	}
	return nil, fmt.Errorf("Hermes provider %q is not present in custom_providers", config.Model.Provider)
}

func (c *LLMClient) Stream(ctx context.Context, history []Message, onChunk func(string)) error {
	messages := make([]llmMessage, 0, len(history)+1)
	messages = append(messages, llmMessage{
		Role:    "system",
		Content: "You are 386GPT, a capable, concise AI assistant. Use plain text that reads clearly in a DOS-style terminal interface.",
	})
	for _, message := range history {
		messages = append(messages, llmMessage{Role: message.Role, Content: message.Content})
	}
	body, err := json.Marshal(completionRequest{Model: c.model, Messages: messages, Stream: true})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("contact %s: %w", c.provider, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("%s returned %s: %s", c.provider, response.Status, strings.TrimSpace(string(detail)))
	}
	if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		var result completionResponse
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			return fmt.Errorf("decode %s response: %w", c.provider, err)
		}
		if result.Error != nil {
			return errors.New(result.Error.Message)
		}
		if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
			return errors.New("provider returned an empty completion")
		}
		onChunk(result.Choices[0].Message.Content)
		return nil
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	received := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var event completionResponse
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decode %s stream: %w", c.provider, err)
		}
		if event.Error != nil {
			return errors.New(event.Error.Message)
		}
		if len(event.Choices) > 0 && event.Choices[0].Delta.Content != "" {
			received = true
			onChunk(event.Choices[0].Delta.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s stream: %w", c.provider, err)
	}
	if !received {
		return errors.New("provider returned an empty completion")
	}
	return nil
}
