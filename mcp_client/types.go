package main

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type LlmProvider string

const (
	OpenAI    LlmProvider = "openai"
	Anthropic LlmProvider = "anthropic"
	Google    LlmProvider = "google"
	Meta      LlmProvider = "meta"
	Mistral   LlmProvider = "mistral"
	DeepSeek  LlmProvider = "deepseek"
	Other     LlmProvider = "other"

	// OTel metric for number of input tokens used by an LLM
	LLMInputTokens = "dagger.io/metrics.llm.input.tokens"

	// OTel metric for number of input tokens read from cache by an LLM
	LLMInputTokensCacheReads = "dagger.io/metrics.llm.input.tokens.cache.reads"

	// OTel metric for number of input tokens written to cache by an LLM
	LLMInputTokensCacheWrites = "dagger.io/metrics.llm.input.tokens.cache.writes"

	// OTel metric for number of output tokens used by an LLM
	LLMOutputTokens = "dagger.io/metrics.llm.output.tokens"

	InstrumentationLibrary = "dagger.io/codegen"

	// Reveal the span all the way up to the top-level parent.
	UIRevealAttr = "dagger.io/ui.reveal"
)

func Tracer() trace.Tracer {
	return otel.Tracer(InstrumentationLibrary)
}

// Reveal can be applied to a span to indicate that this span should
// collapse its children by default.
func Reveal() trace.SpanStartOption {
	return trace.WithAttributes(attribute.Bool(UIRevealAttr, true))
}

type ToolCall struct {
	ID       string   `json:"id"`
	Function FuncCall `json:"function"`
	Type     string   `json:"type"`
}

type FuncCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type TokenUsage struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

// ModelMessage represents a generic message in the LLM conversation
type ModelMessage struct {
	Role        string     `json:"role"`
	Content     any        `json:"content"`
	ToolCalls   []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID  string     `json:"tool_call_id,omitempty"`
	ToolErrored bool       `json:"tool_errored,omitempty"`
	TokenUsage  TokenUsage `json:"token_usage,omitempty"`
}

type LLM struct {
	maxAPICalls int
	apiCalls    int
	Endpoint    *LLMEndpoint

	// If true: has un-synced state
	dirty bool
	// History of messages
	messages []ModelMessage
	// History of tool calls and their result
	calls      map[string]string
	promptVars []string
}

// A frontend for LLM tool calling
type Tool struct {
	Name        string
	Description string
	Schema      map[string]interface{}
}

type LLMEndpoint struct {
	Model    string
	BaseURL  string
	Key      string
	Provider LlmProvider
	Client   LLMClient
}

// LLMClient interface defines the methods that each provider must implement
type LLMClient interface {
	SendQuery(ctx context.Context, history []ModelMessage, tools []Tool) (*LLMResponse, error)
}

type LLMResponse struct {
	Content    string
	ToolCalls  []ToolCall
	TokenUsage TokenUsage
}
