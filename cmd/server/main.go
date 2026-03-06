// Responsibilities here are deliberately minimal:
//   - Load configuration (env vars / .env file)
//   - Construct all dependencies via their constructors
//   - Wire dependencies together (dependency injection)
//   - Register routes
//   - Start the HTTP server
//
// No business logic lives here. Each concern lives in its own package.
package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/voiceline/backend/internal/audio"
	"github.com/voiceline/backend/internal/integrations"
	"github.com/voiceline/backend/internal/llm"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found — reading config from environment")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	llmClient, err := llm.NewOpenAIClient()
	if err != nil {
		log.Fatalf("Failed to initialise LLM client: %v", err)
	}

	sheetsAppender, err := integrations.NewSheetsAppender()
	if err != nil {
		log.Fatalf("Failed to initialise Sheets appender: %v", err)
	}

	processor, err := audio.NewAudioProcessor(llmClient, sheetsAppender)
	if err != nil {
		log.Fatalf("Failed to initialise audio processor: %v", err)
	}

	r := gin.Default()

	// health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// v1 API group
	v1 := r.Group("/v1")
	{
		// process a voice memo: transcribe -> extract note -> push to integration.
		v1.POST("/audio/process", processor.Process)
	}

	log.Printf("VoiceLine backend starting on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
