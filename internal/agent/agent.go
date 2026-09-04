package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	log       *Log
	mu        sync.Mutex
	startTime time.Time
	isSub     bool
}

type Log struct {
	entries []string
	mu      sync.Mutex
}

func (l *Log) Add(entry string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	l.entries = append(l.entries, fmt.Sprintf("[%s] %s", timestamp, entry))
}

func (l *Log) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
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
		log:      &Log{entries: []string{}},
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

	a.log.Add("Agent started")
	a.log.Add(fmt.Sprintf("Task: %s", a.cfg.Prompt))
	a.log.Add(fmt.Sprintf("Mode: %s", a.cfg.Mode))
	a.log.Add(fmt.Sprintf("Model: %s", a.cfg.Model))

	result, err := a.loop(ctx)
	if err != nil {
		a.log.Add(fmt.Sprintf("Agent failed: %v", err))
		return "", err
	}

	a.log.Add(fmt.Sprintf("Agent completed in %s", time.Since(a.startTime)))
	a.log.Add(fmt.Sprintf("Result: %s", result))

	if a.cfg.SaveLog {
		if err := a.saveLog(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save log: %v\n", err)
		}
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

		a.log.Add(fmt.Sprintf("Assistant: %s", truncate(resp.Content, 200)))

		assistantMsg := llm.Message{
			Role:    "assistant",
			Content: resp.Content,
		}
		if len(resp.ToolCalls) > 0 {
			assistantMsg.ToolCalls = resp.ToolCalls
		}
		a.msgs = append(a.msgs, assistantMsg)

		if a.cfg.CompiledRegex != nil && a.cfg.CompiledRegex.MatchString(resp.Content) {
			return resp.Content, nil
		}

		if len(resp.ToolCalls) == 0 {
			if a.cfg.CompiledRegex != nil {
				return "", fmt.Errorf("agent finished without matching verification regex")
			}
			return resp.Content, nil
		}

		for _, tc := range resp.ToolCalls {
			result, err := a.executeTool(ctx, tc)
			if err != nil {
				result = fmt.Sprintf("ERROR: %v", err)
				a.log.Add(fmt.Sprintf("Tool %s error: %v", tc.Name, err))
			} else {
				a.log.Add(fmt.Sprintf("Tool %s: %s", tc.Name, truncate(result, 200)))
			}

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
	a.log.Add(fmt.Sprintf("Spawning sub-agent for task: %s", task))

	subCfg := *a.cfg
	subCfg.Prompt = task
	subCfg.VerificationRegex = ""
	subCfg.CompiledRegex = nil
	subCfg.SaveLog = false
	subCfg.SendLog = false

	sub := &Agent{
		cfg:      &subCfg,
		client:   a.client,
		registry: a.registry,
		todoMgr:  a.todoMgr,
		log:      a.log,
		isSub:    true,
	}

	result, err := sub.loop(ctx)
	if err != nil {
		return fmt.Sprintf("Sub-agent failed: %v", err), nil
	}

	a.log.Add(fmt.Sprintf("Sub-agent completed: %s", truncate(result, 200)))
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

func (a *Agent) saveLog() error {
	logContent := a.log.String()
	return os.WriteFile("log.md", []byte(logContent), 0644)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
