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
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
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
		msg := openai.ChatCompletionMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
		}
		openaiMsgs = append(openaiMsgs, msg)
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

// StreamEvent represents a chunk from streaming responses.
type StreamEvent struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        *Usage
}

// ChatStream sends a streaming chat request and returns a channel of events.
func (c *Client) ChatStream(ctx context.Context, msgs []Message, tools []Tool) (<-chan StreamEvent, error) {
	openaiMsgs := make([]openai.ChatCompletionMessage, 0, len(msgs))
	for _, m := range msgs {
		msg := openai.ChatCompletionMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
		}
		openaiMsgs = append(openaiMsgs, msg)
	}

	req := openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: openaiMsgs,
		Stream:   true,
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

	stream, err := c.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}

	ech := make(chan StreamEvent, 32)
	go func() {
		defer close(ech)
		defer stream.Close()

		for {
			resp, err := stream.Recv()
			if err != nil {
				// Stream finished
				return
			}

			if len(resp.Choices) == 0 {
				continue
			}

			choice := resp.Choices[0]
			event := StreamEvent{
				Content:      choice.Delta.Content,
				FinishReason: string(choice.FinishReason),
			}

			// Accumulate tool calls from delta
			for _, tc := range choice.Delta.ToolCalls {
				event.ToolCalls = append(event.ToolCalls, ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}

			// Usage is only available in the last chunk when stream_options is set
			if resp.Usage != nil {
				event.Usage = &Usage{
					PromptTokens:     resp.Usage.PromptTokens,
					CompletionTokens: resp.Usage.CompletionTokens,
					TotalTokens:      resp.Usage.TotalTokens,
				}
			}

			ech <- event
		}
	}()

	return ech, nil
}
