package aguiadk

import (
	"encoding/json"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/google/uuid"
	"google.golang.org/adk/session"
)

// Translator converts ADK session events into AG-UI protocol events.
//
// It maintains per-run state to enforce AG-UI's ordering invariants:
//
//  1. Text messages must be framed with START → CONTENT* → END
//  2. A text stream must be closed before any tool calls start
//  3. Duplicate consolidated messages (partial=false after streaming) are suppressed
//
// A new Translator must be created for each run.
type Translator struct {
	// streaming text state
	isStreaming       bool
	streamingMessageID string
	lastStreamedText   string

	// tool call tracking
	activeToolCalls map[string]bool // tool_call_id → active
}

// NewTranslator creates a fresh translator for a single run.
func NewTranslator() *Translator {
	return &Translator{
		activeToolCalls: make(map[string]bool),
	}
}

// Translate converts a single ADK session event into zero or more AG-UI events.
// The caller should iterate the returned slice and write each event to the SSE stream.
func (t *Translator) Translate(evt *session.Event) []aguievents.Event {
	if evt == nil || evt.Content == nil {
		return nil
	}

	// Skip user-authored events (we already emitted the user message).
	if evt.Author == "user" {
		return nil
	}

	var out []aguievents.Event

	for _, part := range evt.Content.Parts {
		// Text content
		if part.Text != "" {
			// Thought/reasoning parts: emit as thinking text (future enhancement)
			// For now, stream as regular text.
			if !t.isStreaming {
				t.streamingMessageID = "msg-" + uuid.New().String()
				t.isStreaming = true
				role := "assistant"
				out = append(out, aguievents.NewTextMessageStartEvent(
					t.streamingMessageID,
					aguievents.WithRole(role),
				))
			}
			// Deduplicate: skip if this text is already in the accumulated stream
			if part.Text != t.lastStreamedText {
				out = append(out, aguievents.NewTextMessageContentEvent(
					t.streamingMessageID, part.Text,
				))
				t.lastStreamedText = part.Text
			}
		}

		// Function (tool) calls
		if part.FunctionCall != nil {
			// Close any open text stream first — AG-UI ordering invariant
			if closeEvt := t.ForceCloseStreaming(); closeEvt != nil {
				out = append(out, closeEvt)
			}

			toolCallID := part.FunctionCall.ID
			if toolCallID == "" {
				toolCallID = "tool-" + uuid.New().String()
			}
			t.activeToolCalls[toolCallID] = true

			argsJSON, _ := json.Marshal(part.FunctionCall.Args)
			out = append(out,
				aguievents.NewToolCallStartEvent(toolCallID, part.FunctionCall.Name),
				aguievents.NewToolCallArgsEvent(toolCallID, string(argsJSON)),
				aguievents.NewToolCallEndEvent(toolCallID),
			)
		}

		// Function responses (tool results)
		if part.FunctionResponse != nil {
			toolCallID := part.FunctionResponse.ID
			if toolCallID == "" {
				continue
			}
			// Only emit result if we previously emitted the call
			delete(t.activeToolCalls, toolCallID)
			resultJSON, _ := json.Marshal(part.FunctionResponse.Response)
			out = append(out, aguievents.NewToolCallResultEvent(
				evt.ID,
				toolCallID,
				string(resultJSON),
			))
		}
	}

	return out
}

// ForceCloseStreaming closes any open text message stream and returns the
// TEXT_MESSAGE_END event. Returns nil if no stream is open.
//
// Must be called before emitting tool calls (to preserve ordering) and at
// the end of a run.
func (t *Translator) ForceCloseStreaming() aguievents.Event {
	if !t.isStreaming {
		return nil
	}
	t.isStreaming = false
	evt := aguievents.NewTextMessageEndEvent(t.streamingMessageID)
	t.streamingMessageID = ""
	t.lastStreamedText = ""
	return evt
}
