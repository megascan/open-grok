package sampler

import (
	"encoding/json"
	"fmt"

	"github.com/megascan/open-grok/internal/types"
)

func parseChatSSE(data []byte, st *sseState) []types.StreamEvent {
	var errObj struct {
		Error *struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &errObj) == nil && errObj.Error != nil && errObj.Error.Message != "" {
		return []types.StreamEvent{{Kind: types.StreamError, Err: fmt.Errorf("%s", errObj.Error.Message)}}
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				Reasoning        string `json:"reasoning"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil
	}
	var out []types.StreamEvent
	for _, ch := range chunk.Choices {
		if ch.Delta.Content != "" {
			out = append(out, types.StreamEvent{Kind: types.StreamDelta, Text: ch.Delta.Content})
		}
		think := ch.Delta.ReasoningContent
		if think == "" {
			think = ch.Delta.Reasoning
		}
		if think != "" {
			out = append(out, types.StreamEvent{Kind: types.StreamThinking, Thinking: think})
		}
		for _, tc := range ch.Delta.ToolCalls {
			if st.toolArgs == nil {
				st.toolArgs = map[int]stringBuilder{}
				st.toolIDs = map[int]string{}
				st.toolName = map[int]string{}
			}
			if tc.ID != "" {
				st.toolIDs[tc.Index] = tc.ID
			}
			if tc.Function.Name != "" {
				st.toolName[tc.Index] = tc.Function.Name
			}
			b := st.toolArgs[tc.Index]
			b.WriteString(tc.Function.Arguments)
			st.toolArgs[tc.Index] = b
		}
		if ch.FinishReason == "tool_calls" {
			for idx, name := range st.toolName {
				out = append(out, types.StreamEvent{
					Kind: types.StreamToolCall,
					ToolCall: &types.ToolCall{
						ID:        st.toolIDs[idx],
						Name:      name,
						Arguments: st.toolArgs[idx].String(),
					},
				})
			}
		}
		if ch.FinishReason != "" && ch.FinishReason != "tool_calls" {
			out = append(out, types.StreamEvent{Kind: types.StreamDone, Finish: ch.FinishReason})
		}
	}
	if chunk.Usage != nil {
		out = append(out, types.StreamEvent{
			Kind: types.StreamUsage,
			Usage: &types.Usage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			},
		})
	}
	return out
}

type stringBuilder struct{ s string }

func (b *stringBuilder) WriteString(s string) { b.s += s }
func (b stringBuilder) String() string        { return b.s }

func parseResponsesSSE(data []byte, st *sseState) []types.StreamEvent {
	var ev struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
		Item  *struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			CallID    string `json:"call_id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil
	}
	switch ev.Type {
	case "response.output_text.delta", "response.output_text.delta.done":
		if ev.Delta != "" {
			return []types.StreamEvent{{Kind: types.StreamDelta, Text: ev.Delta}}
		}
	case "response.reasoning_summary_text.delta", "response.reasoning.delta":
		if ev.Delta != "" {
			return []types.StreamEvent{{Kind: types.StreamThinking, Thinking: ev.Delta}}
		}
	case "response.output_item.done", "response.function_call_arguments.done":
		if ev.Item != nil && (ev.Item.Type == "function_call" || ev.Item.Name != "") {
			id := ev.Item.CallID
			if id == "" {
				id = ev.Item.ID
			}
			return []types.StreamEvent{{
				Kind: types.StreamToolCall,
				ToolCall: &types.ToolCall{
					ID:        id,
					Name:      ev.Item.Name,
					Arguments: ev.Item.Arguments,
				},
			}}
		}
	case "response.completed":
		return []types.StreamEvent{{Kind: types.StreamDone, Finish: "stop"}}
	}
	_ = st
	return nil
}

func parseMessagesSSE(data []byte, _ *sseState) []types.StreamEvent {
	var ev struct {
		Type  string `json:"type"`
		Delta *struct {
			Text string `json:"text"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil
	}
	if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Text != "" {
		return []types.StreamEvent{{Kind: types.StreamDelta, Text: ev.Delta.Text}}
	}
	if ev.Type == "message_stop" {
		return []types.StreamEvent{{Kind: types.StreamDone, Finish: "stop"}}
	}
	return nil
}

func parseChatCompletion(body []byte) []types.StreamEvent {
	var errObj struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &errObj) == nil && errObj.Error != nil && errObj.Error.Message != "" {
		return []types.StreamEvent{{Kind: types.StreamError, Err: fmt.Errorf("%s", errObj.Error.Message)}}
	}
	var full struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &full) != nil || len(full.Choices) == 0 {
		return nil
	}
	var out []types.StreamEvent
	ch := full.Choices[0]
	if ch.Message.Content != "" {
		out = append(out, types.StreamEvent{Kind: types.StreamDelta, Text: ch.Message.Content})
	}
	for _, tc := range ch.Message.ToolCalls {
		out = append(out, types.StreamEvent{
			Kind: types.StreamToolCall,
			ToolCall: &types.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return out
}
