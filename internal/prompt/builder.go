package prompt

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/styx-ai/styx/internal/config"
)

func BuildSystem(cfg *config.Config) string {
	var sb strings.Builder

	sb.WriteString("# Styx AI Harness\n\n")
	sb.WriteString("You are Styx, an AI agent operating in a CI/CD environment. ")
	sb.WriteString("You work autonomously through tool calls to accomplish the given task.\n\n")

	sb.WriteString("## Task\n\n")
	sb.WriteString(cfg.Prompt + "\n\n")

	sb.WriteString("## Environment\n\n")
	sb.WriteString(fmt.Sprintf("- **OS**: %s/%s\n", runtime.GOOS, runtime.GOARCH))
	sb.WriteString(fmt.Sprintf("- **Mode**: %s\n", cfg.Mode))
	if cfg.Mode == config.ModeRead {
		sb.WriteString("  - You can READ files and run read-only bash commands\n")
		sb.WriteString("  - You CANNOT write or modify files\n")
	} else {
		sb.WriteString("  - You can READ and WRITE files\n")
		sb.WriteString("  - Use git to create feature branches — do NOT commit directly to main/master\n")
	}

	sb.WriteString("\n## Rules\n\n")
	sb.WriteString("1. Use todos to track progress on complex tasks\n")
	sb.WriteString("2. When modifying code, create a new git branch first (e.g., `git checkout -b styx/task-name`)\n")
	sb.WriteString("3. NEVER commit directly to main or master branch\n")
	sb.WriteString("4. Use the `gh` CLI for all GitHub operations (PRs, issues, repos, actions)\n")
	sb.WriteString("5. After creating or modifying anything via `gh`, always verify the result using `gh` (e.g. `gh pr view`, `gh issue list`)\n")
	sb.WriteString("6. You may spawn sub-agents for parallel work via the spawn_agent tool\n")

	sb.WriteString("\n## Final Requirement\n\n")
	sb.WriteString("When your task is complete, clearly state what was accomplished in your final message.\n")

	sb.WriteString("\n## Context Management\n\n")
	sb.WriteString("If you receive a compacted context summary, continue working from where you left off. ")
	sb.WriteString("The summary will contain your previous progress, blocked items, and completed work.\n")

	return sb.String()
}

func BuildCompactionRequest() string {
	return `The context window is approaching its limit. Please provide a compact summary of your work so far in the following format:

## Summary
**Goal:** [restate the original task]
**Progress:** [what has been accomplished]
**Blocked:** [anything blocked or waiting]
**Remaining:** [what still needs to be done]
**Key Decisions:** [important choices made]
**Files Modified:** [list of files changed/created]
**Current State:** [where you are right now]

Keep this concise but complete enough to continue the work. After this summary, continue working.`
}

func BuildUserPrompt() string {
	return "Begin working on the task. Track your progress with todos."
}
