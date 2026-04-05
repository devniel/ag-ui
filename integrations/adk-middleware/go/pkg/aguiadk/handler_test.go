// Copyright 2026 The ag-ui Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package aguiadk

import (
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"google.golang.org/genai"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	adksession "google.golang.org/adk/session"
)

// fakeAgent builds an ADK agent whose Run yields the given prebuilt events.
func fakeAgent(t *testing.T, name string, events []*adksession.Event) adkagent.Agent {
	t.Helper()
	a, err := adkagent.New(adkagent.Config{
		Name:        name,
		Description: "fake test agent",
		Run: func(_ adkagent.InvocationContext) iter.Seq2[*adksession.Event, error] {
			return func(yield func(*adksession.Event, error) bool) {
				for _, e := range events {
					if !yield(e, nil) {
						return
					}
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return a
}

func newFakeHandler(t *testing.T, events []*adksession.Event) *Handler {
	t.Helper()
	h, err := NewHandler(Config{
		Agent:          fakeAgent(t, "fake_agent", events),
		SessionService: adksession.InMemoryService(),
		AppName:        "fake_app",
		UserID:         "user",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func postRun(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func runInput(thread, run, userText string) string {
	in := aguitypes.RunAgentInput{
		ThreadID: thread,
		RunID:    run,
		Messages: []aguitypes.Message{
			{ID: "m1", Role: "user", Content: userText},
		},
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func TestNewHandlerRequiresAgent(t *testing.T) {
	if _, err := NewHandler(Config{}); err == nil {
		t.Fatal("expected error for missing agent")
	}
}

func TestHandlerRejectsNonPOST(t *testing.T) {
	h := newFakeHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandlerRejectsBadJSON(t *testing.T) {
	h := newFakeHandler(t, nil)
	rr := postRun(t, h, "{not json")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandlerStreamsTextRun(t *testing.T) {
	// Agent yields one plain text event.
	events := []*adksession.Event{
		{
			ID:     "evt-1",
			Author: "fake_agent",
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role:  "model",
					Parts: []*genai.Part{{Text: "hello there"}},
				},
			},
		},
	}
	h := newFakeHandler(t, events)

	rr := postRun(t, h, runInput("thread-1", "run-1", "hi"))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected SSE content-type, got %q", ct)
	}

	body := rr.Body.String()
	// Expected ordering of event types in the SSE stream.
	wantOrder := []string{
		"RUN_STARTED",
		"TEXT_MESSAGE_START",
		"TEXT_MESSAGE_CONTENT",
		"TEXT_MESSAGE_END",
		"RUN_FINISHED",
	}
	assertOrderedContains(t, body, wantOrder)
	if !strings.Contains(body, "hello there") {
		t.Errorf("expected body to contain streamed text, got: %s", body)
	}
}

func TestHandlerStreamsToolCallRun(t *testing.T) {
	events := []*adksession.Event{
		{
			ID:     "evt-call",
			Author: "fake_agent",
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role: "model",
					Parts: []*genai.Part{{
						FunctionCall: &genai.FunctionCall{
							ID:   "call-1",
							Name: "execute_bash",
							Args: map[string]any{"command": "ls"},
						},
					}},
				},
			},
		},
	}
	h := newFakeHandler(t, events)

	rr := postRun(t, h, runInput("thread-2", "run-2", "run ls"))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	assertOrderedContains(t, body, []string{
		"RUN_STARTED",
		"TOOL_CALL_START",
		"TOOL_CALL_ARGS",
		"TOOL_CALL_END",
		"RUN_FINISHED",
	})
	if !strings.Contains(body, "execute_bash") {
		t.Errorf("expected body to contain tool name, got: %s", body)
	}
}

func TestHandlerEmptyMessagesEmitsError(t *testing.T) {
	h := newFakeHandler(t, nil)

	in := aguitypes.RunAgentInput{ThreadID: "t", RunID: "r", Messages: nil}
	b, _ := json.Marshal(in)
	rr := postRun(t, h, string(b))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	assertOrderedContains(t, body, []string{"RUN_STARTED", "RUN_ERROR"})
}

func TestMessageContentString(t *testing.T) {
	if got := messageContentString(aguitypes.Message{Content: "hello"}); got != "hello" {
		t.Errorf("string content: got %q, want %q", got, "hello")
	}
	if got := messageContentString(aguitypes.Message{Content: nil}); got != "" {
		t.Errorf("nil content: got %q, want empty", got)
	}
	structured := map[string]any{"text": "hi"}
	got := messageContentString(aguitypes.Message{Content: structured})
	if !strings.Contains(got, "hi") {
		t.Errorf("structured content: got %q, want JSON containing 'hi'", got)
	}
}

// assertOrderedContains asserts that each of the given substrings appears in
// body in the given order (with other content allowed in between).
func assertOrderedContains(t *testing.T, body string, in []string) {
	t.Helper()
	cursor := 0
	for _, needle := range in {
		idx := strings.Index(body[cursor:], needle)
		if idx < 0 {
			t.Errorf("expected %q after position %d in body; body was:\n%s", needle, cursor, body)
			return
		}
		cursor += idx + len(needle)
	}
}
