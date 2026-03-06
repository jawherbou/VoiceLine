// The central abstraction is the Client interface, which lets you swap in a
// different provider (e.g. Google Gemini, a local Whisper server) without
// touching any calling code.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	openAIWhisperURL = "https://api.groq.com/openai/v1/audio/transcriptions"
	openAIChatURL    = "https://api.groq.com/openai/v1/chat/completions"

	defaultWhisperModel = "whisper-large-v3-turbo"
	defaultChatModel    = "llama-3.3-70b-versatile"
	defaultHTTPTimeout  = 60 * time.Second
	defaultAudioExt     = ".m4a"
)

type Note struct {
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`
	ActionItems   []string `json:"action_items"`
	Tags          []string `json:"tags"`
	RawTranscript string   `json:"raw_transcript"`
	CreatedAt     string   `json:"created_at"`
}

type Client interface {
	Transcribe(ctx context.Context, audioBytes []byte, filename string) (string, error)
	ExtractNote(ctx context.Context, transcript string) (*Note, error)
}

type OpenAIClient struct {
	apiKey       string
	whisperModel string
	chatModel    string
	httpClient   *http.Client
}

func NewOpenAIClient() (*OpenAIClient, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable is required")
	}

	return &OpenAIClient{
		apiKey:       apiKey,
		whisperModel: defaultWhisperModel,
		chatModel:    defaultChatModel,
		httpClient:   &http.Client{Timeout: defaultHTTPTimeout},
	}, nil
}

func (c *OpenAIClient) Transcribe(ctx context.Context, audioBytes []byte, filename string) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	ext := filepath.Ext(filename)
	if ext == "" {
		ext = defaultAudioExt
	}

	part, err := mw.CreateFormFile("file", "audio"+ext)
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}
	part.Write(audioBytes)

	mw.WriteField("model", c.whisperModel)
	mw.WriteField("response_format", "text")
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIWhisperURL, &buf)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenAI API error: %s", string(body))
	}

	return strings.TrimSpace(string(body)), nil
}

func (c *OpenAIClient) ExtractNote(ctx context.Context, transcript string) (*Note, error) {
	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type chatRequest struct {
		Model          string            `json:"model"`
		Messages       []chatMessage     `json:"messages"`
		Temperature    float64           `json:"temperature"`
		ResponseFormat map[string]string `json:"response_format"`
	}

	systemPrompt := `Extract structured notes from the voice transcript. Output a JSON object with fields: title (string), summary (string), action_items (array of strings), tags (array of strings).`

	reqBody := chatRequest{
		Model: c.chatModel,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: transcript},
		},
		Temperature:    0.0,
		ResponseFormat: map[string]string{"type": "json_object"},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIChatURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API error: %s", string(body))
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	var note Note
	if err := json.Unmarshal([]byte(response.Choices[0].Message.Content), &note); err != nil {
		return nil, fmt.Errorf("failed to unmarshal note: %w", err)
	}

	note.RawTranscript = transcript
	note.CreatedAt = time.Now().Format(time.RFC3339)
	return &note, nil
}
