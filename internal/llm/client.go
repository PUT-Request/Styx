package llm

import (
	"context"
	"fmt"
	"math"
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

	backoff := time.Second
	maxBackoff := 30 * time.Second

	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				backoff = time.Duration(math.Min(float64(backoff)*2, float64(maxBackoff)))
			}
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

	return nil, fmt.Errorf("after 5 attempts, last error: %w", lastErr)
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
