package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/styx-ai/styx/internal/llm"
)

type TodoStatus string

const (
	TodoPending TodoStatus = "pending"
	TodoDone    TodoStatus = "done"
)

type Todo struct {
	ID     int        `json:"id"`
	Text   string     `json:"text"`
	Status TodoStatus `json:"status"`
}

type TodoManager struct {
	mu    sync.Mutex
	todos []Todo
}

func NewTodoManager() *TodoManager {
	return &TodoManager{todos: []Todo{}}
}

func (tm *TodoManager) Add(text string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	id := len(tm.todos) + 1
	tm.todos = append(tm.todos, Todo{ID: id, Text: text, Status: TodoPending})
}

func (tm *TodoManager) Update(id int, status TodoStatus) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for i := range tm.todos {
		if tm.todos[i].ID == id {
			tm.todos[i].Status = status
			return nil
		}
	}
	return fmt.Errorf("todo %d not found", id)
}

func (tm *TodoManager) Clear() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.todos = []Todo{}
}

func (tm *TodoManager) List() []Todo {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	result := make([]Todo, len(tm.todos))
	copy(result, tm.todos)
	return result
}

func (tm *TodoManager) String() string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	var sb strings.Builder
	sb.WriteString("[\n")
	for _, t := range tm.todos {
		status := "pending"
		if t.Status == TodoDone {
			status = "done"
		}
		sb.WriteString(fmt.Sprintf("  {\"id\": %d, \"text\": %q, \"status\": %q}\n", t.ID, t.Text, status))
	}
	sb.WriteString("]")
	return sb.String()
}

type FileManager struct{}

func NewFileManager() *FileManager {
	return &FileManager{}
}

const MaxChunkSize = 50 * 1024 // 50KB

func (fm *FileManager) Read(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	if info.Size() > MaxChunkSize {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		truncated := string(data[:MaxChunkSize])
		return truncated + fmt.Sprintf("\n... [TRUNCATED: file is %d bytes, showing first %d]", info.Size(), MaxChunkSize), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (fm *FileManager) Write(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	return os.WriteFile(path, []byte(content), 0644)
}

type BashExecutor struct {
	timeout time.Duration
}

func NewBashExecutor(timeout time.Duration) *BashExecutor {
	return &BashExecutor{timeout: timeout}
}

func (be *BashExecutor) Run(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), be.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	result := string(out)

	if ctx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("command timed out after %s", be.timeout)
	}
	if err != nil {
		return result, fmt.Errorf("%w: %s", err, result)
	}
	return result, nil
}

type ToolRegistry struct {
	todos *TodoManager
	files *FileManager
	bash  *BashExecutor
	mode  string
}

func NewToolRegistry(todos *TodoManager, files *FileManager, bash *BashExecutor, mode string) *ToolRegistry {
	return &ToolRegistry{
		todos: todos,
		files: files,
		bash:  bash,
		mode:  mode,
	}
}

func (tr *ToolRegistry) GetTools() []llm.Tool {
	return tr.buildTools(true)
}

func (tr *ToolRegistry) GetSubAgentTools() []llm.Tool {
	return tr.buildTools(false)
}

func (tr *ToolRegistry) buildTools(includeSpawn bool) []llm.Tool {
	tools := []llm.Tool{
		{
			Name:        "todos_add",
			Description: "Add a new todo item to the task list",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"text": map[string]interface{}{
						"type":        "string",
						"description": "The todo item text",
					},
				},
				"required": []string{"text"},
			},
		},
		{
			Name:        "todos_update",
			Description: "Update the status of a todo item",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "integer",
						"description": "The todo item ID",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"description": "New status: pending or done",
						"enum":        []string{"pending", "done"},
					},
				},
				"required": []string{"id", "status"},
			},
		},
		{
			Name:        "todos_clear",
			Description: "Clear all todo items",
			Schema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "todos_list",
			Description: "List all current todo items",
			Schema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "read_file",
			Description: "Read a file. Files larger than 50KB are truncated.",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute or relative path to the file",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "bash",
			Description: "Execute a shell command. Use git via this tool for version control.",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "The shell command to execute",
					},
				},
				"required": []string{"command"},
			},
		},
	}

	if includeSpawn {
		tools = append(tools, llm.Tool{
			Name:        "spawn_agent",
			Description: "Spawn a sub-agent to handle a specific task. Sub-agents cannot spawn further sub-agents. Returns the agent's final result.",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task": map[string]interface{}{
						"type":        "string",
						"description": "The task description for the sub-agent",
					},
				},
				"required": []string{"task"},
			},
		})
	}

	if tr.mode == "read_write" {
		tools = append(tools, llm.Tool{
			Name:        "write_file",
			Description: "Write content to a file. Creates directories if needed.",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute or relative path to the file",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "The content to write",
					},
				},
				"required": []string{"path", "content"},
			},
		})
	}

	return tools
}

func (tr *ToolRegistry) Execute(name string, args json.RawMessage) (string, error) {
	switch name {
	case "todos_add":
		var p struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return "", err
		}
		tr.todos.Add(p.Text)
		return fmt.Sprintf("Added todo: %s", p.Text), nil

	case "todos_update":
		var p struct {
			ID     int    `json:"id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return "", err
		}
		if err := tr.todos.Update(p.ID, TodoStatus(p.Status)); err != nil {
			return "", err
		}
		return fmt.Sprintf("Updated todo %d to %s", p.ID, p.Status), nil

	case "todos_clear":
		tr.todos.Clear()
		return "Cleared all todos", nil

	case "todos_list":
		return tr.todos.String(), nil

	case "read_file":
		var p struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return "", err
		}
		return tr.files.Read(p.Path)

	case "write_file":
		var p struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return "", err
		}
		if err := tr.files.Write(p.Path, p.Content); err != nil {
			return "", err
		}
		return fmt.Sprintf("Wrote %d bytes to %s", len(p.Content), p.Path), nil

	case "bash":
		var p struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return "", err
		}
		return tr.bash.Run(p.Command)

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}
