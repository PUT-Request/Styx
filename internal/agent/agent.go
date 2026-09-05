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

// appendMsg appends a message to the agent's message history with proper mutex locking.
func (a *Agent) appendMsg(msg llm.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.msgs = append(a.msgs, msg)
}

// getMsgs returns a copy of the current message history.
func (a *Agent) getMsgs() []llm.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]llm.Message, len(a.msgs))
	copy(result, a.msgs)
	return result
}

// setMsgs replaces the message history with a new slice (with mutex protection).
func (a *Agent) setMsgs(msgs []llm.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.msgs = msgs
}

func (a *Agent) Run(ctx context.Context) (string, error) {
	a.startTime = time.Now()

	ctx, cancel := context.WithTimeout(ctx, a.cfg.MaxDuration)
	defer cancel()

	a.appendMsg(llm.Message{
		Role:    "system",
		Content: prompt.BuildSystem(a.cfg),
	})
	a.appendMsg(llm.Message{
		Role:    "user",
		Content: prompt.BuildUserPrompt(),
	})

	result, err := a.loop(ctx)
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

		// Use a copy of messages to avoid race conditions with compact()
		msgsCopy := a.getMsgs()

		if a.shouldCompact(msgsCopy) {
			a.compact(ctx, msgsCopy)
		}

		resp, err := a.client.Chat(ctx, msgsCopy, toolDefs)
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
		a.appendMsg(assistantMsg)

		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		for _, tc := range resp.ToolCalls {
			result, err := a.executeTool(ctx, tc)
			if err != nil {
				result = fmt.Sprintf("ERROR: %v", err)
			}

			a.appendMsg(llm.Message{
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
	// Check if context is already done
	if ctx.Err() != nil {
		return "", fmt.Errorf("context already done: %w", ctx.Err())
	}

	subCfg := *a.cfg
	subCfg.Prompt = task

	// Create a new registry for the sub-agent to avoid shared mutable state
	subTodoMgr := tools.NewTodoManager()
	subFileMgr := tools.NewFileManager()
	subBashExec := tools.NewBashExecutor(5 * time.Minute)
	subRegistry := tools.NewToolRegistry(subTodoMgr, subFileMgr, subBashExec, string(subCfg.Mode))

	sub := &Agent{
		cfg:      &subCfg,
		client:   a.client,
		registry: subRegistry,
		todoMgr:  subTodoMgr,
		isSub:    true,
	}

	systemPrompt := prompt.BuildSystem(sub.cfg)
	sub.appendMsg(llm.Message{
		Role:    "system",
		Content: systemPrompt,
	})
	sub.appendMsg(llm.Message{
		Role:    "user",
		Content: prompt.BuildUserPrompt(),
	})

	result, err := sub.loop(ctx)
	if err != nil {
		return fmt.Sprintf("Sub-agent failed: %v", err), nil
	}

	return fmt.Sprintf("Sub-agent result: %s", result), nil
}

func (a *Agent) shouldCompact(msgs []llm.Message) bool {
	total := 0
	for _, m := range msgs {
		total += len(m.Content)
	}
	maxChars := a.cfg.MaxContext * 3
	return total > int(float64(maxChars)*0.9)
}

func (a *Agent) compact(ctx context.Context, _ []llm.Message) {
	a.mu.Lock()
	// Double-check we have enough messages
	if len(a.msgs) < 2 {
		a.mu.Unlock()
		return
	}

	systemPrompt := a.msgs[0].Content
	originalUser := a.msgs[1].Content

	// We have the messages under the lock, release it before calling LLM
	a.mu.Unlock()

	// Build compacted messages - use a copy since we released the lock
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

	a.mu.Lock()
	a.msgs = []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: originalUser},
		{Role: "assistant", Content: resp.Content},
	}
	a.mu.Unlock()
}