package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"golang.org/x/oauth2/google"
)

func main() {
	ctx := context.Background()

	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		panic(err)
	}
	tok, err := creds.TokenSource.Token()
	if err != nil {
		panic(err)
	}
	//fmt.Printf("Access token (expires %s): %s\n\n", tok.Expiry.Format(time.RFC3339), tok.AccessToken)

	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	location := os.Getenv("GOOGLE_CLOUD_LOCATION")
	if project == "" || location == "" {
		panic("GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION must be set")
	}

	baseURL := fmt.Sprintf(
		"https://aiplatform.googleapis.com/v1/projects/%s/locations/%s/endpoints/openapi",
		project, location,
	)

	cfg := openai.DefaultConfig(tok.AccessToken)
	cfg.BaseURL = baseURL

	client := openai.NewClientWithConfig(cfg)

	// Seed randomness for mock weather.
	rand.Seed(time.Now().UnixNano())

	req := openai.ChatCompletionRequest{
		Model: openai.Gemini3ProPreview,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleUser,
				Content: "What's the weather in Seattle today, should I bring an umbrella, " +
					"and what time is it right now? Use tools as needed.",
			},
		},
		Tools: []openai.Tool{
			{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        "get_weather",
					Description: "Get current weather for a city.",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"city": map[string]any{
								"type":        "string",
								"description": "City name, e.g. Seattle",
							},
						},
						"required": []string{"city"},
					},
				},
			},
			{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        "get_current_time",
					Description: "Get the current time. Optionally specify an IANA timezone like America/Los_Angeles.",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"timezone": map[string]any{
								"type":        "string",
								"description": "Optional IANA timezone. Defaults to local time.",
							},
						},
					},
				},
			},
		},
		ToolChoice: "auto",
		ExtraBody: map[string]any{
			"google": map[string]any{
				"thinking_config": map[string]any{
					"include_thoughts": true,
				},
			},
		},
	}

	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		panic(err)
	}

	assistantMsg := resp.Choices[0].Message
	fmt.Println("Reply:", assistantMsg.Content)
	if len(assistantMsg.MultiContent) > 0 {
		fmt.Printf("Thought signature: %+v\n", assistantMsg.MultiContent[0].ExtraPart)
	}

	if len(assistantMsg.ToolCalls) == 0 {
		fmt.Println("Tool calls: none")
		return
	}

	b, _ := json.MarshalIndent(assistantMsg.ToolCalls, "", "  ")
	fmt.Printf("Tool calls (assistant):\n%s\n", string(b))

	// Append assistant tool-call message to the conversation.
	req.Messages = append(req.Messages, assistantMsg)

	// Mock tool execution for each call and append tool responses.
	for _, tc := range assistantMsg.ToolCalls {
		switch tc.Function.Name {
		case "get_weather":
			city := "unknown"
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &struct {
				City *string `json:"city"`
			}{City: &city})

			payload := map[string]any{
				"city":       city,
				"temp_c":     5 + rand.Intn(25), // 5..29
				"condition":  []string{"rain", "sunny", "cloudy", "windy"}[rand.Intn(4)],
				"umbrella":   rand.Intn(2) == 0,
				"updated_at": time.Now().Format(time.RFC3339),
			}
			contentBytes, _ := json.Marshal(payload)
			req.Messages = append(req.Messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    string(contentBytes),
				ToolCallID: tc.ID,
			})
		case "get_current_time":
			tz := ""
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &struct {
				Timezone *string `json:"timezone"`
			}{Timezone: &tz})

			now := time.Now()
			if tz != "" {
				if loc, err := time.LoadLocation(tz); err == nil {
					now = now.In(loc)
				}
			}
			payload := map[string]any{
				"timezone":   tz,
				"timestamp":  now.Format(time.RFC3339),
				"human_time": now.Format(time.RFC1123),
			}
			contentBytes, _ := json.Marshal(payload)
			req.Messages = append(req.Messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    string(contentBytes),
				ToolCallID: tc.ID,
			})
		default:
			// Unknown tool; return a stub so the model can react.
			payload := map[string]any{"error": "unknown tool"}
			contentBytes, _ := json.Marshal(payload)
			req.Messages = append(req.Messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    string(contentBytes),
				ToolCallID: tc.ID,
			})
		}
	}

	// Second call to let the model use the tool outputs.
	resp2, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		panic(err)
	}
	finalMsg := resp2.Choices[0].Message
	fmt.Println("Final answer:", finalMsg.Content)
	if len(finalMsg.MultiContent) > 0 {
		fmt.Printf("Final thought signature: %+v\n", finalMsg.MultiContent[0].ExtraPart)
	}
}
