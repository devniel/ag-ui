package aguiadk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	aguisse "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"google.golang.org/genai"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	adksession "google.golang.org/adk/session"
)

// Config configures the AG-UI handler.
type Config struct {
	// Agent is the ADK root agent to run.
	Agent adkagent.Agent
	// SessionService stores conversation history. Use session.InMemoryService() for dev.
	SessionService adksession.Service
	// AppName is the ADK application name (used as the session namespace).
	// Defaults to Agent.Name() if empty.
	AppName string
	// UserID is the default ADK user ID. Defaults to "user".
	UserID string
	// Logger is optional — set to log.Default() for verbose tracing.
	Logger *log.Logger
}

// Handler is an http.Handler that accepts AG-UI RunAgentInput POST requests
// and streams AG-UI SSE events as the ADK agent produces output.
type Handler struct {
	cfg    Config
	runner *runner.Runner
	writer *aguisse.SSEWriter
}

// NewHandler creates an AG-UI HTTP handler wrapping an ADK agent.
func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Agent == nil {
		return nil, fmt.Errorf("aguiadk: Agent is required")
	}
	if cfg.SessionService == nil {
		cfg.SessionService = adksession.InMemoryService()
	}
	if cfg.AppName == "" {
		cfg.AppName = cfg.Agent.Name()
	}
	if cfg.UserID == "" {
		cfg.UserID = "user"
	}

	r, err := runner.New(runner.Config{
		AppName:        cfg.AppName,
		Agent:          cfg.Agent,
		SessionService: cfg.SessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("aguiadk: creating runner: %w", err)
	}

	return &Handler{
		cfg:    cfg,
		runner: r,
		writer: aguisse.NewSSEWriter(),
	}, nil
}

// ServeHTTP handles an AG-UI request. Expects POST with a JSON RunAgentInput body.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	h.log("incoming: %s", body)

	var input aguitypes.RunAgentInput
	if err := json.Unmarshal(body, &input); err != nil {
		http.Error(w, "decode input: "+err.Error(), http.StatusBadRequest)
		return
	}

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()

	// RUN_STARTED
	h.emit(ctx, w, aguievents.NewRunStartedEvent(input.ThreadID, input.RunID))

	if len(input.Messages) == 0 {
		h.emit(ctx, w, aguievents.NewRunErrorEvent("no messages"))
		return
	}

	// Ensure ADK session exists (create is idempotent for this purpose)
	_, err = h.cfg.SessionService.Create(ctx, &adksession.CreateRequest{
		AppName:   h.cfg.AppName,
		UserID:    h.cfg.UserID,
		SessionID: input.ThreadID,
	})
	if err != nil {
		h.log("session create (may already exist): %v", err)
	}

	// Build the new user message from the last AG-UI message
	lastMsg := input.Messages[len(input.Messages)-1]
	userContent := &genai.Content{
		Role: "user",
		Parts: []*genai.Part{
			{Text: messageContentString(lastMsg)},
		},
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Run the ADK agent — returns iter.Seq2 of session events
	stream := h.runner.Run(runCtx, h.cfg.UserID, input.ThreadID, userContent, adkagent.RunConfig{})

	translator := NewTranslator()

	for evt, err := range stream {
		if err != nil {
			h.log("runner error: %v", err)
			h.emit(ctx, w, aguievents.NewRunErrorEvent(err.Error()))
			return
		}
		for _, aguiEvt := range translator.Translate(evt) {
			h.emit(ctx, w, aguiEvt)
		}
	}

	// Close any open text stream
	if closeEvt := translator.ForceCloseStreaming(); closeEvt != nil {
		h.emit(ctx, w, closeEvt)
	}

	// RUN_FINISHED
	h.emit(ctx, w, aguievents.NewRunFinishedEvent(input.ThreadID, input.RunID))
}

// emit writes an SSE event and flushes the response writer.
func (h *Handler) emit(ctx context.Context, w http.ResponseWriter, evt aguievents.Event) {
	if err := h.writer.WriteEvent(ctx, w, evt); err != nil {
		h.log("write event: %v", err)
		return
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (h *Handler) log(format string, args ...any) {
	if h.cfg.Logger != nil {
		h.cfg.Logger.Printf("[aguiadk] "+format, args...)
	}
}

// messageContentString extracts the text content from an AG-UI message.
// AG-UI Message.Content is `any` to support both string and structured content.
func messageContentString(m aguitypes.Message) string {
	if s, ok := m.Content.(string); ok {
		return s
	}
	if m.Content != nil {
		if b, err := json.Marshal(m.Content); err == nil {
			return string(b)
		}
	}
	return ""
}
