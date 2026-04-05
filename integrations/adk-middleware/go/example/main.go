// Example: a minimal Google ADK agent exposed over AG-UI for CopilotKit.
//
// Run:
//
//	export GOOGLE_API_KEY=...
//	go run ./example
//
// Then point a CopilotKit runtime at http://localhost:8000/
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/ag-ui-protocol/ag-ui/integrations/adk-middleware/go/pkg/aguiadk"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

func main() {
	ctx := context.Background()

	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		log.Fatal("GOOGLE_API_KEY env var is required")
	}

	model, err := gemini.NewModel(ctx, "gemini-2.5-flash", &genai.ClientConfig{
		APIKey: apiKey,
	})
	if err != nil {
		log.Fatalf("create model: %v", err)
	}

	agent, err := llmagent.New(llmagent.Config{
		Name:        "assistant",
		Model:       model,
		Description: "A helpful assistant.",
		Instruction: "You are a helpful assistant.",
		Tools:       []tool.Tool{},
	})
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}

	handler, err := aguiadk.NewHandler(aguiadk.Config{
		Agent:          agent,
		SessionService: session.InMemoryService(),
		Logger:         log.Default(),
	})
	if err != nil {
		log.Fatalf("create handler: %v", err)
	}

	// CORS for browser-based CopilotKit clients in development
	cors := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	log.Println("AG-UI ADK server listening on :8000")
	if err := http.ListenAndServe(":8000", cors(handler)); err != nil {
		log.Fatal(err)
	}
}
