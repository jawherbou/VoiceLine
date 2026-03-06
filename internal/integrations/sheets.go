// Adding a new integration (Notion, HubSpot, a webhook …) means:
//  1. Create a new file in this package (e.g. notion.go)
//  2. Implement the NoteAppender interface on a new struct
//  3. Register it in main.go — no other code needs to change
//
// Simulates a strategy pattern, where the handler depends only on the NoteAppender interface,
package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/voiceline/backend/internal/llm"
)

type NoteAppender interface {
	Append(ctx context.Context, note *llm.Note) (resourceURL string, err error)
}

const (
	sheetsBaseURL = "https://sheets.googleapis.com/v4/spreadsheets"

	defaultSheetRange = "Notes!A:F"

	sheetsHTTPTimeout = 15 * time.Second
)

type SheetsAppender struct {
	sheetID    string
	sheetRange string
	token      string
	httpClient *http.Client
}

func NewSheetsAppender() (*SheetsAppender, error) {
	sheetID := os.Getenv("GOOGLE_SHEET_ID")
	if sheetID == "" {
		return nil, fmt.Errorf("GOOGLE_SHEET_ID environment variable is not set")
	}

	token := os.Getenv("GOOGLE_SHEETS_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("GOOGLE_SHEETS_TOKEN environment variable is not set")
	}

	sheetRange := os.Getenv("GOOGLE_SHEET_RANGE")
	if sheetRange == "" {
		sheetRange = defaultSheetRange
	}

	return &SheetsAppender{
		sheetID:    sheetID,
		sheetRange: sheetRange,
		token:      token,
		httpClient: &http.Client{Timeout: sheetsHTTPTimeout},
	}, nil
}

func (s *SheetsAppender) Append(ctx context.Context, note *llm.Note) (string, error) {
	row := []any{
		note.CreatedAt,
		note.Title,
		note.Summary,
		strings.Join(note.ActionItems, "; "),
		strings.Join(note.Tags, ", "),
		note.RawTranscript,
	}

	payload := map[string]any{
		"values": [][]any{row},
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshalling sheet row payload: %w", err)
	}

	url := fmt.Sprintf(
		"%s/%s/values/%s:append?valueInputOption=USER_ENTERED",
		sheetsBaseURL, s.sheetID, s.sheetRange,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("building sheets request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sheets HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sheets API returned %d: %s", resp.StatusCode, string(respBody))
	}

	sheetURL := fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s", s.sheetID)
	return sheetURL, nil
}
