package llm

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/sashabaranov/go-openai"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type Response struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type Client struct {
	client *openai.Client
	model  string
}

func NewClient(apiKey, endpoint, model string) *Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = endpoint
	return &Client{
		client: openai.NewClientWithConfig(cfg),
		model:  model,
	}
}

func (c *Client) Chat(ctx context.Context, msgs []Message, tools []Tool) (*Response, error) {
	var lastErr error

	const maxAttempts = 6
	backoff := 2 * time.Second
	maxBackoff := 2 * time.Minute

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			jitter := time.Duration(float64(backoff) * (0.5 + rand.Float64()*0.5))
			sleep := time.Duration(math.Min(float64(jitter), float64(maxBackoff)))

			fmt.Fprintf(os.Stderr, "Retry %d/%d after %s (error: %v)\n", attempt, maxAttempts-1, sleep, lastErr)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(sleep):
			}

			backoff = time.Duration(math.Min(float64(backoff)*2.5, float64(maxBackoff)))
		}

		resp, err := c.doChat(ctx, msgs, tools)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	return nil, fmt.Errorf("after %d attempts, last error: %w", maxAttempts, lastErr)
}

func (c *Client) doChat(ctx context.Context, msgs []Message, tools []Tool) (*Response, error) {
	openaiMsgs := make([]openai.ChatCompletionMessage, 0, len(msgs))
	for _, m := range msgs {
		openaiMsgs = append(openaiMsgs, openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	req := openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: openaiMsgs,
	}

	if len(tools) > 0 {
		openaiTools := make([]openai.Tool, 0, len(tools))
		for _, t := range tools {
			openaiTools = append(openaiTools, openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Schema,
				},
			})
		}
		req.Tools = openaiTools
	}

	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	r := &Response{
		Content: resp.Choices[0].Message.Content,
		Usage: Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}

	for _, tc := range resp.Choices[0].Message.ToolCalls {
		r.ToolCalls = append(r.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	return r, nil
}

type Tool struct {
	Name        string
	Description string
	Schema      map[string]interface{}
}
