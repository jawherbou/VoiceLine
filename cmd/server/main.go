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
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/voiceline/backend/internal/audio"
	"github.com/voiceline/backend/internal/integrations"
	"github.com/voiceline/backend/internal/llm"
	"github.com/voiceline/backend/internal/ratelimit"
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

	// Rate limiter — token bucket, per IP and per user
	limiter := ratelimit.New(ratelimit.DefaultIPConfig, ratelimit.DefaultUserConfig)
	// Apply rate limiting globally
	r.Use(limiter.Middleware())

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

	// wrap gin in an http.Server so we can shut it down gracefully
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// start server in a goroutine so it doesn't block the shutdown listener
	go func() {
		log.Printf("VoiceLine backend starting on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server: %v", err)
		}
	}()

	// Graceful shutdown
	// Wait for SIGINT (Ctrl+C) or SIGTERM (Docker/K8s stop signal)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down — draining in-flight requests...")

	// give active HTTP requests 10 seconds to finish
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Println("Shutdown complete")
}
