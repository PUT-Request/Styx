package tools

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// EditFileTool performs surgical text replacements in files.
type EditFileTool struct{}

// EditResult describes the outcome of an edit operation.
type EditResult struct {
	Path         string `json:"path"`
	Replacements int    `json:"replacements"`
	LinesChanged int    `json:"lines_changed"`
}

func (e *EditFileTool) Execute(path, oldText, newText string) (*EditResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	content := string(data)

	if !strings.Contains(content, oldText) {
		return nil, fmt.Errorf("old_text not found in %s", path)
	}

	// Count occurrences
	count := strings.Count(content, oldText)
	
	// Replace all occurrences
	newContent := strings.ReplaceAll(content, oldText, newText)

	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return nil, fmt.Errorf("writing file: %w", err)
	}

	// Count approximate lines changed
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	linesChanged := len(newLines) - len(oldLines)
	if linesChanged < 0 {
		linesChanged = -linesChanged
	}

	return &EditResult{
		Path:         path,
		Replacements: count,
		LinesChanged: linesChanged,
	}, nil
}

// PatchFileTool applies a unified diff patch to a file.
type PatchFileTool struct{}

func (p *PatchFileTool) Execute(path, patch string) (string, error) {
	// Write patch to temp file
	tmpFile, err := os.CreateTemp("", "styx-patch-*.patch")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(patch); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("writing patch: %w", err)
	}
	tmpFile.Close()

	// Apply patch
	// git apply works on files in a git repo, so we use patch command instead
	cmd := exec.Command("patch", "-p1", "--no-backup-if-mismatch", "-i", tmpFile.Name())
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()

	if err != nil {
		return string(out), fmt.Errorf("patch failed: %w", err)
	}

	return fmt.Sprintf("Patch applied to %s\n%s", path, string(out)), nil
}

// InsertFileTool inserts text at a specific line number.
type InsertFileTool struct{}

func (i *InsertFileTool) Execute(path string, line int, text string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	if line < 1 || line > len(lines)+1 {
		return "", fmt.Errorf("line %d out of range (file has %d lines)", line, len(lines))
	}

	// Insert at position (1-indexed)
	insertLines := strings.Split(text, "\n")
	newLines := make([]string, 0, len(lines)+len(insertLines))
	newLines = append(newLines, lines[:line-1]...)
	newLines = append(newLines, insertLines...)
	newLines = append(newLines, lines[line-1:]...)

	if err := os.WriteFile(path, []byte(strings.Join(newLines, "\n")), 0644); err != nil {
		return "", fmt.Errorf("writing file: %w", err)
	}

	return fmt.Sprintf("Inserted %d lines at line %d in %s", len(insertLines), line, path), nil
}
