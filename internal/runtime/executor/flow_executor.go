package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// FlowExecutor handles generation requests for Google Flow models.
type FlowExecutor struct {
	cfg *config.Config
}

// NewFlowExecutor creates a new FlowExecutor instance.
func NewFlowExecutor(cfg *config.Config) *FlowExecutor {
	return &FlowExecutor{cfg: cfg}
}

// Identifier returns the provider identifier.
func (e *FlowExecutor) Identifier() string {
	return "flow"
}

// RequestToFormat reports the upstream request format.
func (e *FlowExecutor) RequestToFormat(_ cliproxyexecutor.Request, _ cliproxyexecutor.Options) sdktranslator.Format {
	return sdktranslator.FormatOpenAI
}

// CountTokens estimates the token count for a Flow generation request.
func (e *FlowExecutor) CountTokens(ctx context.Context, _ *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	prompt := extractLastPrompt(req.Payload)
	count := len(strings.Fields(prompt))
	if count < 1 {
		count = 1
	}
	usageJSON := helps.BuildOpenAIUsageJSON(int64(count))
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	return cliproxyexecutor.Response{
		Payload: usageJSON,
		Headers: headers,
	}, nil
}

// HttpRequest injects Flow credentials into the supplied request and executes it.
func (e *FlowExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("flow executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if auth != nil {
		client, err := helps.NewFlowClient(e.cfg, auth)
		if err == nil {
			at, _, _, errTokens := client.EnsureTokens(ctx)
			if errTokens == nil && at != "" {
				httpReq.Header.Set("X-Goog-AuthUser", "0")
			}
		}
	}
	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

// Refresh refreshes Google Flow session tokens if needed.
func (e *FlowExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	if auth == nil {
		return nil, fmt.Errorf("flow executor: auth is nil")
	}
	client, errClient := helps.NewFlowClient(e.cfg, auth)
	if errClient != nil {
		return auth, nil
	}
	at, fSid, bl, errTokens := client.EnsureTokens(ctx)
	if errTokens != nil {
		return auth, nil
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["at_token"] = at
	auth.Metadata["f_sid"] = fSid
	auth.Metadata["build_label"] = bl
	return auth, nil
}

// Execute dispatches inbound requests to images, videos, or chat completions.
func (e *FlowExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	path := strings.ToLower(helps.PayloadRequestPath(opts))

	if strings.Contains(path, "/images/") {
		return e.executeImages(ctx, auth, req, opts)
	}

	if strings.Contains(path, "/videos/") {
		return e.executeVideos(ctx, auth, req, opts)
	}

	// Route /v1/chat/completions to corresponding image/video pipeline
	model := strings.ToLower(req.Model)
	if strings.Contains(model, "video") || strings.Contains(model, "veo") {
		return e.executeChatVideo(ctx, auth, req, opts)
	}

	return e.executeChatImage(ctx, auth, req, opts)
}

// ExecuteStream sends the generation result formatted as an OpenAI-compatible SSE stream.
func (e *FlowExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	out := make(chan cliproxyexecutor.StreamChunk, 4)

	go func() {
		defer close(out)
		resp, err := e.Execute(ctx, auth, req, opts)
		if err != nil {
			errPayload, _ := json.Marshal(map[string]any{
				"error": map[string]any{
					"message": err.Error(),
					"type":    "flow_error",
				},
			})
			out <- cliproxyexecutor.StreamChunk{
				Payload: errPayload,
			}
			return
		}

		id := "chatcmpl-" + uuid.New().String()
		created := time.Now().Unix()

		var content string
		if gjson.GetBytes(resp.Payload, "choices.0.message.content").Exists() {
			content = gjson.GetBytes(resp.Payload, "choices.0.message.content").String()
		} else if gjson.GetBytes(resp.Payload, "data.0.url").Exists() {
			content = fmt.Sprintf("![Generated Image](%s)", gjson.GetBytes(resp.Payload, "data.0.url").String())
		} else if gjson.GetBytes(resp.Payload, "url").Exists() {
			content = fmt.Sprintf("[Generated Video](%s)", gjson.GetBytes(resp.Payload, "url").String())
		} else {
			content = string(resp.Payload)
		}

		chunkData := fmt.Sprintf("data: {\"id\":\"%s\",\"object\":\"chat.completion.chunk\",\"created\":%d,\"model\":\"%s\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%s},\"finish_reason\":null}]}\n\n",
			id, created, req.Model, strconvQuote(content))
		out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunkData)}

		doneChunk := fmt.Sprintf("data: {\"id\":\"%s\",\"object\":\"chat.completion.chunk\",\"created\":%d,\"model\":\"%s\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
			id, created, req.Model)
		out <- cliproxyexecutor.StreamChunk{Payload: []byte(doneChunk)}
	}()

	headers := make(http.Header)
	headers.Set("Content-Type", "text/event-stream")
	return &cliproxyexecutor.StreamResult{Chunks: out, Headers: headers}, nil
}

func (e *FlowExecutor) executeChatImage(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	prompt := extractLastPrompt(req.Payload)
	imageReqPayload, _ := sjson.SetBytes([]byte("{}"), "prompt", prompt)
	imageReqPayload, _ = sjson.SetBytes(imageReqPayload, "model", req.Model)

	imageReq := req
	imageReq.Payload = imageReqPayload

	imgResp, err := e.executeImages(ctx, auth, imageReq, opts)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}

	imageURL := gjson.GetBytes(imgResp.Payload, "data.0.url").String()
	markdown := fmt.Sprintf("![Generated Image](%s)\n\n*Generated with Google Flow %s*", imageURL, req.Model)

	return formatChatCompletion(req.Model, markdown), nil
}

func (e *FlowExecutor) executeChatVideo(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	prompt := extractLastPrompt(req.Payload)
	vidReqPayload, _ := sjson.SetBytes([]byte("{}"), "prompt", prompt)
	vidReqPayload, _ = sjson.SetBytes(vidReqPayload, "model", req.Model)

	vidReq := req
	vidReq.Payload = vidReqPayload

	vidResp, err := e.executeVideos(ctx, auth, vidReq, opts)
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}

	videoURL := gjson.GetBytes(vidResp.Payload, "url").String()
	thumbURL := gjson.GetBytes(vidResp.Payload, "thumbnail_url").String()
	markdown := fmt.Sprintf("Here is your generated video from Google Flow:\n\n[![Video Thumbnail](%s)](%s)\n\n[Direct Video Link](%s)\n\n*Generated with Google Flow %s*",
		thumbURL, videoURL, videoURL, req.Model)

	return formatChatCompletion(req.Model, markdown), nil
}

func extractLastPrompt(payload []byte) string {
	messages := gjson.GetBytes(payload, "messages").Array()
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Get("role").String() == "user" {
			content := msg.Get("content")
			if content.Type == gjson.String {
				return content.String()
			}
			if content.IsArray() {
				var sb strings.Builder
				for _, part := range content.Array() {
					if part.Get("type").String() == "text" {
						sb.WriteString(part.Get("text").String())
					}
				}
				if sb.Len() > 0 {
					return sb.String()
				}
			}
		}
	}
	return gjson.GetBytes(payload, "prompt").String()
}

func formatChatCompletion(model, content string) cliproxyexecutor.Response {
	id := "chatcmpl-" + uuid.New().String()
	created := time.Now().Unix()

	respMap := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     10,
			"completion_tokens": 20,
			"total_tokens":      30,
		},
	}

	bytes, _ := json.Marshal(respMap)
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	return cliproxyexecutor.Response{
		Payload: bytes,
		Headers: headers,
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
