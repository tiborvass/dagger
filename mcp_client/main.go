package main

import (
	"context"
	"fmt"
	"io"
	slog "log"
	"os"
	"strings"
	"time"

	"dagger.io/dagger/telemetry"
	"github.com/chzyer/readline"
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

	fmt.Fprintf(os.Stderr, "🔄 Starting sync loop with %d messages\n", len(llm.messages))

	// Log the last message for context
	if len(llm.messages) > 0 {
		lastMsg := llm.messages[len(llm.messages)-1]
		fmt.Fprintf(os.Stderr, "📝 Last message: Role=%s, Content=%s\n",
			lastMsg.Role, truncateString(fmt.Sprintf("%v", lastMsg.Content), 100))
	}

	for {
		if llm.maxAPICalls > 0 && llm.apiCalls >= llm.maxAPICalls {
			fmt.Fprintf(os.Stderr, "⚠️ Reached API call limit: %d\n", llm.apiCalls)
			return nil, fmt.Errorf("reached API call limit: %d", llm.apiCalls)
		}

		fmt.Fprintf(os.Stderr, "📞 Making API call #%d\n", llm.apiCalls+1)
		llm.apiCalls++

		// Get the latest tools from MCP
		fmt.Fprintf(os.Stderr, "🧰 Requesting tools list from MCP\n")
		toolsRequest := mcp.ListToolsRequest{}
		toolsResponse, err := mcpClient.ListTools(ctx, toolsRequest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to list tools: %v\n", err)
			return nil, fmt.Errorf("failed to list tools: %w", err)
		}

		fmt.Fprintf(os.Stderr, "🔧 Received %d tools from MCP\n", len(toolsResponse.Tools))
		for i, tool := range toolsResponse.Tools {
			fmt.Fprintf(os.Stderr, "  🛠️ Tool #%d: %s - %s\n", i+1, tool.Name, tool.Description)
			fmt.Fprintf(os.Stderr, "     Schema Type: %s, Required: %v\n",
				tool.InputSchema.Type, tool.InputSchema.Required)
			fmt.Fprintf(os.Stderr, "     Properties: %+v\n", tool.InputSchema.Properties)
		}

		// Convert MCP tools to LLM tools
		tools := convertMCPToolsToLLMTools(toolsResponse.Tools)

		// Send query to LLM
		fmt.Fprintf(os.Stderr, "🤖 Sending query to LLM with %d messages and %d tools\n",
			len(llm.messages), len(tools))

		// Log the tools being sent
		for i, tool := range tools {
			fmt.Fprintf(os.Stderr, "  🔨 Tool #%d for LLM: %s - %s\n",
				i+1, tool.Name, tool.Description)
			fmt.Fprintf(os.Stderr, "     Schema: %+v\n", tool.Schema)
		}

		fmt.Fprintf(os.Stderr, "⏳ Waiting for LLM response...\n")
		res, err := llm.Endpoint.Client.SendQuery(ctx, llm.messages, tools)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to send query to LLM: %v\n", err)
			return nil, err
		}

		fmt.Fprintf(os.Stderr, "📩 Received response from LLM with %d tool calls\n", len(res.ToolCalls))
		fmt.Fprintf(os.Stderr, "  📊 Token usage: %+v\n", res.TokenUsage)
		fmt.Fprintf(os.Stderr, "  💬 Content: %s\n", truncateString(fmt.Sprintf("%v", res.Content), 200))

		// Add the model reply to the history
		llm.messages = append(llm.messages, ModelMessage{
			Role:       "assistant",
			Content:    res.Content,
			ToolCalls:  res.ToolCalls,
			TokenUsage: res.TokenUsage,
		})

		// If no tool calls, we're done
		if len(res.ToolCalls) == 0 {
			fmt.Fprintf(os.Stderr, "✅ No tool calls, exiting sync loop\n")
			break
		}

		// Process tool calls
		for i, toolCall := range res.ToolCalls {
			fmt.Fprintf(os.Stderr, "🔨 Processing tool call #%d: %s (ID: %s)\n",
				i+1, toolCall.Function.Name, toolCall.ID)
			fmt.Fprintf(os.Stderr, "  📄 Arguments: %s\n", toolCall.Function.Arguments)

			toolFound := false
			for _, tool := range tools {
				if tool.Name == toolCall.Function.Name {
					toolFound = true
					fmt.Fprintf(os.Stderr, "  🚀 Executing tool: %s\n", tool.Name)
					fmt.Fprintf(os.Stderr, "  ⏳ Waiting for tool execution...\n")

					result, isError := executeMCPToolCall(ctx, mcpClient, tool, toolCall)

					if isError {
						fmt.Fprintf(os.Stderr, "  ⚠️ Tool execution resulted in error: %s\n", result)
					} else {
						fmt.Fprintf(os.Stderr, "  ✅ Tool execution successful, result: %s\n",
							truncateString(result, 200))
					}

					// Add tool call result to history
					llm.calls[toolCall.ID] = result
					fmt.Fprintf(os.Stderr, "  📝 Adding tool result to message history (ID: %s)\n", toolCall.ID)
					llm.messages = append(llm.messages, ModelMessage{
						Role:        "user", // Anthropic only allows tool calls in user messages
						Content:     result,
						ToolCallID:  toolCall.ID,
						ToolErrored: isError,
					})
				}
			}

			if !toolFound {
				fmt.Fprintf(os.Stderr, "  ⚠️ Tool not found: %s\n", toolCall.Function.Name)
			}
		}

		fmt.Fprintf(os.Stderr, "🔄 Continuing sync loop with %d messages\n", len(llm.messages))
	}

	llm.dirty = false
	fmt.Fprintf(os.Stderr, "🏁 Sync completed successfully\n")
	return llm, nil
}

// Convert MCP tools to LLM tools with detailed logging
func convertMCPToolsToLLMTools(mcpTools []mcp.Tool) []Tool {
	var tools []Tool
	fmt.Fprintf(os.Stderr, "🔍 Converting %d MCP tools to LLM tools\n", len(mcpTools))

	for i, mcpTool := range mcpTools {
		// Parse the input schema
		var schema = map[string]interface{}{}
		schema["type"] = mcpTool.InputSchema.Type
		schema["properties"] = mcpTool.InputSchema.Properties
		schema["required"] = mcpTool.InputSchema.Required

		fmt.Fprintf(os.Stderr, "🔧 Tool #%d: %s\n", i+1, mcpTool.Name)
		fmt.Fprintf(os.Stderr, "  📝 Description: %s\n", mcpTool.Description)
		fmt.Fprintf(os.Stderr, "  📋 Schema type: %s\n", mcpTool.InputSchema.Type)
		fmt.Fprintf(os.Stderr, "  🔑 Required fields: %v\n", mcpTool.InputSchema.Required)
		fmt.Fprintf(os.Stderr, "  🏷️ Properties: %+v\n", mcpTool.InputSchema.Properties)

		// Create a new tool
		tools = append(tools, Tool{
			Name:        mcpTool.Name,
			Description: mcpTool.Description,
			Schema:      schema,
		})
	}

	fmt.Fprintf(os.Stderr, "✅ Converted %d tools successfully\n", len(tools))
	return tools
}

// Execute an MCP tool call with detailed logging
func executeMCPToolCall(ctx context.Context, mcpClient client.MCPClient, tool Tool, toolCall ToolCall) (string, bool) {
	fmt.Fprintf(os.Stderr, "⚙️ Executing tool call: %s (ID: %s)\n", tool.Name, toolCall.ID)
	fmt.Fprintf(os.Stderr, "  📄 Arguments: %s\n", toolCall.Function.Arguments)

	// Create execute request
	executeRequest := mcp.CallToolRequest{
		Request: mcp.Request{
			Method: "tools/call",
		},
	}
	executeRequest.Params.Name = toolCall.Function.Name
	executeRequest.Params.Arguments = toolCall.Function.Arguments

	// Execute the tool
	fmt.Fprintf(os.Stderr, "  📡 Sending tool call request to MCP\n")
	fmt.Fprintf(os.Stderr, "  ⏳ Waiting for MCP response...\n")
	executeResponse, err := mcpClient.CallTool(ctx, executeRequest)
	if err != nil {
		errResponse := err.Error()
		fmt.Fprintf(os.Stderr, "  ❌ Tool call failed with error: %s\n", errResponse)
		return errResponse, true
	}

	fmt.Fprintf(os.Stderr, "  📬 Received tool call response with %d content items\n", len(executeResponse.Content))

	if len(executeResponse.Content) != 1 {
		fmt.Fprintf(os.Stderr, "  ⚠️ Unexpected response format: expected 1 content item, got %d\n",
			len(executeResponse.Content))
		panic("expected MCP response to have exactly one content")
	}
	result := executeResponse.Content[0].(mcp.TextContent).Text
	if executeResponse.IsError {
		fmt.Fprintf(os.Stderr, "  ❌ Tool call completed with error\n")
		fmt.Fprintf(os.Stderr, "  ⚠️ Error result (%d chars): %s\n",
			len(result), truncateString(result, 200))
	} else {
		fmt.Fprintf(os.Stderr, "  ✅ Tool call completed successfully\n")
		fmt.Fprintf(os.Stderr, "  📊 Success result (%d chars): %s\n",
			len(result), truncateString(result, 200))
	}
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
		return
	}

	// Create MCP client
	c, err := client.NewStdioMCPClient(
		os.Args[1],
		os.Environ(),
		os.Args[2:]...,
	)
	if err != nil {
		slog.Fatalf("Failed to create client: %v\n", err)
		return
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
		slog.Printf("Failed to initialize: %v\n", err)
		return
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
		slog.Printf("Failed to list tools: %v\n", err)
		return
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

	rl, err := readline.New("> ")
	if err != nil {
		slog.Printf("could not create readline: %v\n", err)
	}
	rl.CaptureExitSignal()
	defer rl.Close()

	fmt.Println("Enter your prompts (type 'exit' or 'quit' to quit):")
	for {
		line, err := rl.Readline()
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "failed to read line: %v", err)
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			break
		}
		readline.AddHistory(line)

		// Add user prompt
		llm = llm.WithPrompt(ctx, line)

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
}
