package tools

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GrepTool searches file contents for a regex pattern.
type GrepTool struct{}

func (g *GrepTool) Execute(pattern, path, glob string, ignoreCase bool) (string, error) {
	if path == "" {
		path = "."
	}

	flags := "-n"
	if ignoreCase {
		flags += "i"
	}

	args := []string{flags, "--color=never"}
	if glob != "" {
		args = append(args, "--glob", glob)
	}
	args = append(args, pattern, path)

	cmd := exec.Command("grep", args...)
	out, err := cmd.Output()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "No matches found.", nil
		}
		return "", fmt.Errorf("grep failed: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// FindFilesTool finds files matching a glob pattern.
type FindFilesTool struct{}

func (f *FindFilesTool) Execute(pattern, path string) (string, error) {
	if path == "" {
		path = "."
	}

	fullPattern := filepath.Join(path, pattern)
	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		return "", fmt.Errorf("invalid pattern: %w", err)
	}

	if len(matches) == 0 {
		return "No files found.", nil
	}

	var sb strings.Builder
	for _, m := range matches {
		sb.WriteString(m + "\n")
	}

	return strings.TrimSpace(sb.String()), nil
}

// GitDiffTool gets the diff for a git ref or working tree.
type GitDiffTool struct{}

func (g *GitDiffTool) Execute(ref string, path string) (string, error) {
	var args []string
	if ref == "" || ref == "working" {
		args = []string{"diff"}
	} else if ref == "staged" {
		args = []string{"diff", "--cached"}
	} else {
		args = []string{"diff", ref}
	}

	if path != "" {
		args = append(args, "--", path)
	}

	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()

	if err != nil {
		return "", fmt.Errorf("git diff failed: %w", err)
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		return "No changes.", nil
	}

	// Truncate if too large (over 50KB)
	if len(result) > 50*1024 {
		lines := strings.Split(result, "\n")
		truncated := lines[:200]
		result = strings.Join(truncated, "\n")
		result += fmt.Sprintf("\n... [TRUNCATED: diff is %d lines, showing first 200]", len(lines))
	}

	return result, nil
}

// GrepLine represents a single grep match.
type GrepLine struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// GrepToolRich returns structured grep results.
type GrepToolRich struct{}

func (g *GrepToolRich) Execute(pattern, path, glob string, ignoreCase bool) ([]GrepLine, error) {
	if path == "" {
		path = "."
	}

	// Use grep -rn to get file:line:content format
	args := []string{"-rn", "--color=never"}
	if ignoreCase {
		args = append(args, "-i")
	}
	if glob != "" {
		args = append(args, "--glob", glob)
	}
	args = append(args, pattern, path)

	cmd := exec.Command("grep", args...)
	out, err := cmd.Output()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return []GrepLine{}, nil
		}
		return nil, fmt.Errorf("grep failed: %w", err)
	}

	var results []GrepLine
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		// Parse "file:linenum:text"
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		var linenum int
		fmt.Sscanf(parts[1], "%d", &linenum)
		results = append(results, GrepLine{
			File: parts[0],
			Line: linenum,
			Text: parts[2],
		})
	}

	return results, nil
}

// WalkFiles walks a directory tree and returns all files.
func WalkFiles(root string, maxFiles int) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, path)
		if maxFiles > 0 && len(files) >= maxFiles {
			return fmt.Errorf("max files reached")
		}
		return nil
	})
	if err != nil && err.Error() != "max files reached" {
		return nil, err
	}
	return files, nil
}
