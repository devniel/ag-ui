// Copyright 2026 The ag-ui Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package aguiadk

import (
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"google.golang.org/genai"

	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
)

// textEvent builds a session.Event carrying a single text part.
func textEvent(author, text string) *session.Event {
	return &session.Event{
		ID:     "evt-" + text,
		Author: author,
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Role:  "model",
				Parts: []*genai.Part{{Text: text}},
			},
		},
	}
}

// toolCallEvent builds a session.Event carrying a single function call part.
func toolCallEvent(id, name string, args map[string]any) *session.Event {
	return &session.Event{
		ID:     "evt-call-" + id,
		Author: "agent",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{{
					FunctionCall: &genai.FunctionCall{ID: id, Name: name, Args: args},
				}},
			},
		},
	}
}

// toolResponseEvent builds a session.Event carrying a single function response part.
func toolResponseEvent(id, name string, resp map[string]any) *session.Event {
	return &session.Event{
		ID:     "evt-resp-" + id,
		Author: "agent",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Role: "user",
				Parts: []*genai.Part{{
					FunctionResponse: &genai.FunctionResponse{ID: id, Name: name, Response: resp},
				}},
			},
		},
	}
}

func types(events []aguievents.Event) []aguievents.EventType {
	out := make([]aguievents.EventType, len(events))
	for i, e := range events {
		out[i] = e.Type()
	}
	return out
}

func TestTranslatorNilAndEmpty(t *testing.T) {
	tr := NewTranslator()
	if got := tr.Translate(nil); got != nil {
		t.Errorf("expected nil for nil event, got %v", got)
	}
	if got := tr.Translate(&session.Event{}); got != nil {
		t.Errorf("expected nil for event with no content, got %v", got)
	}
}

func TestTranslatorSkipsUserEvents(t *testing.T) {
	tr := NewTranslator()
	got := tr.Translate(textEvent("user", "hello from user"))
	if got != nil {
		t.Errorf("expected user-authored events to be skipped, got %v", got)
	}
}

func TestTranslatorTextStreamingStartsOnceAndDedupes(t *testing.T) {
	tr := NewTranslator()

	// First text chunk → START + CONTENT
	first := tr.Translate(textEvent("agent", "Hello"))
	if want := []aguievents.EventType{
		aguievents.EventTypeTextMessageStart,
		aguievents.EventTypeTextMessageContent,
	}; !equalTypes(types(first), want) {
		t.Errorf("first chunk types = %v, want %v", types(first), want)
	}

	// Second text chunk with new content → only CONTENT (no new START)
	second := tr.Translate(textEvent("agent", " world"))
	if want := []aguievents.EventType{aguievents.EventTypeTextMessageContent}; !equalTypes(types(second), want) {
		t.Errorf("second chunk types = %v, want %v", types(second), want)
	}

	// Duplicate chunk (same text as lastStreamedText) → suppressed entirely
	dup := tr.Translate(textEvent("agent", " world"))
	if len(dup) != 0 {
		t.Errorf("duplicate text should be suppressed, got %v", types(dup))
	}
}

func TestTranslatorTextToolOrderingInvariant(t *testing.T) {
	tr := NewTranslator()

	_ = tr.Translate(textEvent("agent", "thinking..."))

	// Now a tool call arrives — translator must emit TEXT_MESSAGE_END
	// before any TOOL_CALL_* events.
	got := tr.Translate(toolCallEvent("call-1", "shell", map[string]any{"cmd": "ls"}))
	want := []aguievents.EventType{
		aguievents.EventTypeTextMessageEnd,
		aguievents.EventTypeToolCallStart,
		aguievents.EventTypeToolCallArgs,
		aguievents.EventTypeToolCallEnd,
	}
	if !equalTypes(types(got), want) {
		t.Errorf("tool-after-text types = %v, want %v", types(got), want)
	}
}

func TestTranslatorToolCallWithoutPriorText(t *testing.T) {
	tr := NewTranslator()
	got := tr.Translate(toolCallEvent("call-1", "shell", map[string]any{"cmd": "pwd"}))
	want := []aguievents.EventType{
		aguievents.EventTypeToolCallStart,
		aguievents.EventTypeToolCallArgs,
		aguievents.EventTypeToolCallEnd,
	}
	if !equalTypes(types(got), want) {
		t.Errorf("tool-only types = %v, want %v", types(got), want)
	}
}

func TestTranslatorToolResponseEmitsResult(t *testing.T) {
	tr := NewTranslator()
	// Prime with a call so the translator has the id in its active map
	_ = tr.Translate(toolCallEvent("call-1", "shell", map[string]any{}))

	got := tr.Translate(toolResponseEvent("call-1", "shell", map[string]any{"stdout": "ok"}))
	want := []aguievents.EventType{aguievents.EventTypeToolCallResult}
	if !equalTypes(types(got), want) {
		t.Errorf("tool result types = %v, want %v", types(got), want)
	}
}

func TestTranslatorToolResponseWithoutIDSkipped(t *testing.T) {
	tr := NewTranslator()
	got := tr.Translate(toolResponseEvent("", "shell", map[string]any{"stdout": "ok"}))
	if len(got) != 0 {
		t.Errorf("response without id should be skipped, got %v", types(got))
	}
}

func TestTranslatorForceCloseStreaming(t *testing.T) {
	tr := NewTranslator()

	// No stream open → nil
	if evt := tr.ForceCloseStreaming(); evt != nil {
		t.Errorf("expected nil when no stream open, got %v", evt)
	}

	// Open a stream, then close
	_ = tr.Translate(textEvent("agent", "hi"))
	evt := tr.ForceCloseStreaming()
	if evt == nil {
		t.Fatal("expected close event, got nil")
	}
	if evt.Type() != aguievents.EventTypeTextMessageEnd {
		t.Errorf("expected TextMessageEnd, got %s", evt.Type())
	}

	// Closing again should be a no-op
	if evt := tr.ForceCloseStreaming(); evt != nil {
		t.Errorf("expected nil on repeat close, got %v", evt)
	}
}

func equalTypes(a, b []aguievents.EventType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
