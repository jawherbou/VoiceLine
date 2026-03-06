// It wires together the LLM transcription/extraction and the integration
// layer, but remains decoupled from their concrete implementations via interfaces.
package audio

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/voiceline/backend/internal/integrations"
	"github.com/voiceline/backend/internal/llm"
)

const (
	maxAudioSize   = 25 << 20
	audioFormField = "audio"
)

type AudioProcessor struct {
	llmClient llm.Client
	appender  integrations.NoteAppender
}

func NewAudioProcessor(client llm.Client, appender integrations.NoteAppender) (*AudioProcessor, error) {
	if client == nil {
		return nil, fmt.Errorf("llm.Client must not be nil")
	}
	if appender == nil {
		return nil, fmt.Errorf("integrations.NoteAppender must not be nil")
	}
	return &AudioProcessor{
		llmClient: client,
		appender:  appender,
	}, nil
}

func (p *AudioProcessor) Process(c *gin.Context) {
	file, header, err := c.Request.FormFile(audioFormField)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("audio file required (multipart field: %q)", audioFormField),
		})
		return
	}
	defer file.Close()

	if header.Size > maxAudioSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("file too large — max allowed is %d MB", maxAudioSize>>20),
		})
		return
	}

	audioBytes := make([]byte, header.Size)
	if _, err := file.Read(audioBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read uploaded audio"})
		return
	}

	transcript, err := p.llmClient.Transcribe(c.Request.Context(), audioBytes, header.Filename)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "transcription failed: " + err.Error()})
		return
	}

	note, err := p.llmClient.ExtractNote(c.Request.Context(), transcript)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "note extraction failed: " + err.Error()})
		return
	}

	resourceURL, err := p.appender.Append(c.Request.Context(), note)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"note": note,
			"integration_warning": fmt.Sprintf(
				"note extracted but failed to write to integration: %s", err.Error(),
			),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"note":         note,
		"resource_url": resourceURL,
	})
}
