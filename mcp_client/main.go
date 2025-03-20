package main

import (
	"bufio"
	"context"
	"fmt"
	slog "log"
	"os"
	"time"

	"dagger.io/dagger/telemetry"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// Sync with detailed object logging
func (llm *LLM) Sync(ctx context.Context, mcpClient client.MCPClient) (*LLM, error) {
	if !llm.dirty {
		return llm, nil
	}
	if len(llm.messages) == 0 {
		// dirty but no messages, possibly just a state change, nothing to do
		// until a prompt is given
		return llm, nil
	}


	// Log the last message for context
	if len(llm.messages) > 0 {
		lastMsg := llm.messages[len(llm.messages)-1]
		_ = lastMsg
	}

	for {
		if llm.maxAPICalls > 0 && llm.apiCalls >= llm.maxAPICalls {
			return nil, fmt.Errorf("reached API call limit: %d", llm.apiCalls)
		}

		llm.apiCalls++

		// Get the latest tools from MCP
		toolsRequest := mcp.ListToolsRequest{}
		toolsResponse, err := mcpClient.ListTools(ctx, toolsRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to list tools: %w", err)
		}

		// Convert MCP tools to LLM tools
		tools := convertMCPToolsToLLMTools(toolsResponse.Tools)

		// Send query to LLM
		res, err := llm.Endpoint.Client.SendQuery(ctx, llm.messages, tools)
		if err != nil {
			return nil, err
		}


		// Add the model reply to the history
		llm.messages = append(llm.messages, ModelMessage{
			Role:       "assistant",
			Content:    res.Content,
			ToolCalls:  res.ToolCalls,
			TokenUsage: res.TokenUsage,
		})

		// If no tool calls, we're done
		if len(res.ToolCalls) == 0 {
			break
		}

		// Process tool calls
		for _, toolCall := range res.ToolCalls {
			toolFound := false
			for _, tool := range tools {
				if tool.Name == toolCall.Function.Name {
					toolFound = true

					result, isError := executeMCPToolCall(ctx, mcpClient, tool, toolCall)

					// Add tool call result to history
					llm.calls[toolCall.ID] = result
					llm.messages = append(llm.messages, ModelMessage{
						Role:        "user", // Anthropic only allows tool calls in user messages
						Content:     result,
						ToolCallID:  toolCall.ID,
						ToolErrored: isError,
					})
				}
			}

			if !toolFound {
			}
		}

	}

	llm.dirty = false
	return llm, nil
}

// Convert MCP tools to LLM tools with detailed logging
func convertMCPToolsToLLMTools(mcpTools []mcp.Tool) []Tool {
	var tools []Tool

	for _, mcpTool := range mcpTools {
		// Parse the input schema
		var schema = map[string]interface{}{}
		schema["type"] = mcpTool.InputSchema.Type
		schema["properties"] = mcpTool.InputSchema.Properties
		schema["required"] = mcpTool.InputSchema.Required


		// Create a new tool
		tools = append(tools, Tool{
			Name:        mcpTool.Name,
			Description: mcpTool.Description,
			Schema:      schema,
		})
	}

	return tools
}

// Execute an MCP tool call with detailed logging
func executeMCPToolCall(ctx context.Context, mcpClient client.MCPClient, tool Tool, toolCall ToolCall) (string, bool) {

	// Create execute request
	executeRequest := mcp.CallToolRequest{
		Request: mcp.Request{
			Method: "tools/call",
		},
	}
	executeRequest.Params.Name = toolCall.Function.Name
	executeRequest.Params.Arguments = toolCall.Function.Arguments

	// Execute the tool
	executeResponse, err := mcpClient.CallTool(ctx, executeRequest)
	if err != nil {
		errResponse := err.Error()
		return errResponse, true
	}


	if len(executeResponse.Content) != 1 {
		panic("expected MCP response to have exactly one content")
	}
	result := executeResponse.Content[0].(mcp.TextContent).Text
	return result, executeResponse.IsError
}

// Helper function to truncate long strings for logging
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Create a new LLM instance
func NewLLM(endpoint *LLMEndpoint, maxAPICalls int) *LLM {
	return &LLM{
		Endpoint:    endpoint,
		maxAPICalls: maxAPICalls,
		messages:    []ModelMessage{},
		calls:       make(map[string]string),
		dirty:       false,
	}
}

// Add a system prompt to the LLM
func (llm *LLM) WithSystemPrompt(prompt string) *LLM {
	llm.messages = append(llm.messages, ModelMessage{
		Role:    "system",
		Content: prompt,
	})
	llm.dirty = true
	return llm
}

// Add a user prompt to the LLM
func (llm *LLM) WithPrompt(ctx context.Context, prompt string) *LLM {
	// Handle prompt variables if needed
	if len(llm.promptVars) > 0 {
		prompt = os.Expand(prompt, func(key string) string {
			// Iterate through vars array taking elements in pairs, looking
			// for a key that matches the template variable being expanded
			for i := 0; i < len(llm.promptVars)-1; i += 2 {
				if llm.promptVars[i] == key {
					return llm.promptVars[i+1]
				}
			}
			// If vars array has odd length and the last key has no value,
			// return empty string when that key is looked up
			if len(llm.promptVars)%2 == 1 && llm.promptVars[len(llm.promptVars)-1] == key {
				return ""
			}
			return key
		})
	}

	// Add telemetry if needed
	func() {
		ctx, span := Tracer().Start(ctx, "LLM prompt", Reveal(), trace.WithAttributes(
			attribute.String("dagger.io/ui.actor.emoji", "🧑"),
			attribute.String("dagger.io/ui.message", "sent"),
		))
		defer span.End()
		stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
			log.String("dagger.io/content.type", "text/markdown"))
		defer stdio.Close()
		fmt.Fprint(stdio.Stdout, prompt)
	}()

	// Add the prompt to the message history
	llm.messages = append(llm.messages, ModelMessage{
		Role:    "user",
		Content: prompt,
	})
	llm.dirty = true
	return llm
}

// Get the last assistant message
func (llm *LLM) GetLastResponse() string {
	for i := len(llm.messages) - 1; i >= 0; i-- {
		if llm.messages[i].Role == "assistant" {
			return llm.messages[i].Content.(string)
		}
	}
	return ""
}

func main() {
	// Get the OpenAI API key from environment
	openaiAPIKey := os.Getenv("OPENAI_API_KEY")
	if openaiAPIKey == "" {
		slog.Fatal("OPENAI_API_KEY environment variable is required")
	}

	// Create MCP client
	c, err := client.NewStdioMCPClient(
		os.Args[1],
		os.Environ(),
		os.Args[2:]...,
	)
	if err != nil {
		slog.Fatalf("Failed to create client: %v", err)
	}
	defer c.Close()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Initialize the client
	fmt.Println("Initializing client...")
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcp-client",
		Version: "1.0.0",
	}

	initResult, err := c.Initialize(ctx, initRequest)
	if err != nil {
		slog.Fatalf("Failed to initialize: %v", err)
	}
	fmt.Printf(
		"Initialized with server: %s %s\n\n",
		initResult.ServerInfo.Name,
		initResult.ServerInfo.Version,
	)

	// List Tools
	// DEBUG
	fmt.Println("Available tools:")
	toolsRequest := mcp.ListToolsRequest{}
	tools, err := c.ListTools(ctx, toolsRequest)
	if err != nil {
		slog.Fatalf("Failed to list tools: %v", err)
	}
	for _, tool := range tools.Tools {
		fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
	}
	fmt.Println()

	// Create OpenAI client
	endpoint := &LLMEndpoint{
		Model:    "gpt-4o",
		Provider: OpenAI,
		Key:      openaiAPIKey,
		Client:   newOpenAIClient(&LLMEndpoint{Model: "gpt-4o", Key: openaiAPIKey, Provider: OpenAI}, ""),
	}

	// Create LLM instance
	llm := NewLLM(endpoint, 100)

	// Add system prompt
	systemPrompt := "You are a helpful AI assistant. You can use tools to accomplish the user's requests. SHOW THE RAW OUTPUT OF THE TOOLS"
	llm = llm.WithSystemPrompt(systemPrompt)

	// Main input loop
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Enter your prompts (type 'exit' to quit):")
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		input := scanner.Text()
		if input == "exit" {
			break
		}

		// Add user prompt
		llm = llm.WithPrompt(ctx, input)

		// Sync with LLM
		llm, err = llm.Sync(ctx, c)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		// Print response
		fmt.Println("\nAssistant:")
		fmt.Println(llm.GetLastResponse())
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		slog.Fatalf("Error reading input: %v", err)
	}
}
