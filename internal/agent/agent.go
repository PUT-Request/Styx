package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/styx-ai/styx/internal/config"
	"github.com/styx-ai/styx/internal/llm"
	"github.com/styx-ai/styx/internal/prompt"
	"github.com/styx-ai/styx/internal/tools"
)

type Agent struct {
	cfg       *config.Config
	client    *llm.Client
	registry  *tools.ToolRegistry
	todoMgr   *tools.TodoManager
	msgs      []llm.Message
	mu        sync.Mutex
	startTime time.Time
	isSub     bool
	evtFn     EventFunc
}

// EventType describes the kind of streaming event.
type EventType string

const (
	EventText       EventType = "text"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventError      EventType = "error"
	EventSummary    EventType = "summary"
)

// Event is emitted during streaming to report progress.
type Event struct {
	Type    EventType
	Content string
	Tool    string // tool name for tool_call/tool_result
}

// EventFunc is called for each streaming event.
type EventFunc func(Event)

// SetEventFunc sets the callback for streaming events.
func (a *Agent) SetEventFunc(fn EventFunc) {
	a.evtFn = fn
}

func (a *Agent) emit(evt Event) {
	if a.evtFn != nil {
		a.evtFn(evt)
	}
}

func New(cfg *config.Config) *Agent {
	todoMgr := tools.NewTodoManager()
	fileMgr := tools.NewFileManager()
	bashExec := tools.NewBashExecutor(5 * time.Minute)
	registry := tools.NewToolRegistry(todoMgr, fileMgr, bashExec, string(cfg.Mode))
	return &Agent{
		cfg:      cfg,
		client:   llm.NewClient(cfg.APIKey, cfg.APIEndpoint, cfg.Model),
		registry: registry,
		todoMgr:  todoMgr,
	}
}

func (a *Agent) Run(ctx context.Context) (string, error) {
	a.startTime = time.Now()

	ctx, cancel := context.WithTimeout(ctx, a.cfg.MaxDuration)
	defer cancel()

	systemPrompt := prompt.BuildSystem(a.cfg)
	a.msgs = []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt.BuildUserPrompt()},
	}

	var result string
	var err error

	if a.evtFn != nil {
		result, err = a.loopStream(ctx)
	} else {
		result, err = a.loop(ctx)
	}

	if err != nil {
		return "", err
	}

	return result, nil
}

func (a *Agent) loop(ctx context.Context) (string, error) {
	var toolDefs []llm.Tool
	if a.isSub {
		toolDefs = a.registry.GetSubAgentTools()
	} else {
		toolDefs = a.registry.GetTools()
	}

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("max working time exceeded: %s", a.cfg.MaxWorkingTime)
		default:
		}

		if a.shouldCompact() {
			a.compact(ctx)
		}

		resp, err := a.client.Chat(ctx, a.msgs, toolDefs)
		if err != nil {
			return "", fmt.Errorf("LLM error: %w", err)
		}

		assistantMsg := llm.Message{
			Role:    "assistant",
			Content: resp.Content,
		}
		if len(resp.ToolCalls) > 0 {
			assistantMsg.ToolCalls = resp.ToolCalls
		}
		a.msgs = append(a.msgs, assistantMsg)

		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		for _, tc := range resp.ToolCalls {
			result, err := a.executeTool(ctx, tc)
			if err != nil {
				result = fmt.Sprintf("ERROR: %v", err)
			}

			a.msgs = append(a.msgs, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}
}

func (a *Agent) loopStream(ctx context.Context) (string, error) {
	var toolDefs []llm.Tool
	if a.isSub {
		toolDefs = a.registry.GetSubAgentTools()
	} else {
		toolDefs = a.registry.GetTools()
	}

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("max working time exceeded: %s", a.cfg.MaxWorkingTime)
		default:
		}

		if a.shouldCompact() {
			a.compact(ctx)
		}

		stream, err := a.client.ChatStream(ctx, a.msgs, toolDefs)
		if err != nil {
			return "", fmt.Errorf("LLM error: %w", err)
		}

		var content string
		var toolCalls []llm.ToolCall
		toolCallMap := make(map[string]*llm.ToolCall)

		for evt := range stream {
			if evt.Content != "" {
				content += evt.Content
				a.emit(Event{Type: EventText, Content: evt.Content})
			}

			for _, tc := range evt.ToolCalls {
				if existing, ok := toolCallMap[tc.ID]; ok {
					// Accumulate arguments for streaming tool calls
					existing.Arguments += tc.Arguments
				} else {
					newTC := llm.ToolCall{
						ID:        tc.ID,
						Name:      tc.Name,
						Arguments: tc.Arguments,
					}
					toolCallMap[tc.ID] = &newTC
				}
			}

			if evt.Usage != nil {
				// Could track usage here
			}
		}

		// Collect final tool calls
		for _, tc := range toolCallMap {
			toolCalls = append(toolCalls, *tc)
		}

		assistantMsg := llm.Message{
			Role:    "assistant",
			Content: content,
		}
		if len(toolCalls) > 0 {
			assistantMsg.ToolCalls = toolCalls
		}
		a.msgs = append(a.msgs, assistantMsg)

		if len(toolCalls) == 0 {
			return content, nil
		}

		for _, tc := range toolCalls {
			a.emit(Event{Type: EventToolCall, Tool: tc.Name, Content: tc.Arguments})

			result, err := a.executeTool(ctx, tc)
			if err != nil {
				result = fmt.Sprintf("ERROR: %v", err)
			}

			a.emit(Event{Type: EventToolResult, Tool: tc.Name, Content: result})

			a.msgs = append(a.msgs, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}
}

func (a *Agent) executeTool(ctx context.Context, tc llm.ToolCall) (string, error) {
	if tc.Name == "spawn_agent" {
		var p struct {
			Task string `json:"task"`
		}
		if err := json.Unmarshal([]byte(tc.Arguments), &p); err != nil {
			return "", err
		}
		return a.spawnSubAgent(ctx, p.Task)
	}
	return a.registry.Execute(tc.Name, json.RawMessage(tc.Arguments))
}

func (a *Agent) spawnSubAgent(ctx context.Context, task string) (string, error) {
	subCfg := *a.cfg
	subCfg.Prompt = task

	sub := &Agent{
		cfg:      &subCfg,
		client:   a.client,
		registry: a.registry,
		todoMgr:  a.todoMgr,
		isSub:    true,
	}

	systemPrompt := prompt.BuildSystem(sub.cfg)
	sub.msgs = []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt.BuildUserPrompt()},
	}

	result, err := sub.loop(ctx)
	if err != nil {
		return fmt.Sprintf("Sub-agent failed: %v", err), nil
	}

	return fmt.Sprintf("Sub-agent result: %s", result), nil
}

func (a *Agent) shouldCompact() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	total := 0
	for _, m := range a.msgs {
		total += len(m.Content)
	}
	maxChars := a.cfg.MaxContext * 3
	return total > int(float64(maxChars)*0.9)
}

func (a *Agent) compact(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()

	systemPrompt := a.msgs[0].Content
	originalUser := a.msgs[1].Content

	msgs := append([]llm.Message{}, a.msgs...)
	msgs = append(msgs, llm.Message{
		Role:    "user",
		Content: prompt.BuildCompactionRequest(),
	})

	compactCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	resp, err := a.client.Chat(compactCtx, msgs, nil)
	if err != nil || resp.Content == "" {
		return
	}

	a.msgs = []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: originalUser},
		{Role: "assistant", Content: resp.Content},
	}
}
