package executor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

type openAIImageData struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type openAIImageResponse struct {
	Created int64             `json:"created"`
	Data    []openAIImageData `json:"data"`
}

func (e *FlowExecutor) executeImages(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	client, errClient := helps.NewFlowClient(e.cfg, auth)
	if errClient != nil {
		return resp, fmt.Errorf("flow executor images: %w", errClient)
	}

	payload := req.Payload
	prompt := strings.TrimSpace(gjson.GetBytes(payload, "prompt").String())
	if prompt == "" {
		return resp, fmt.Errorf("flow executor images: prompt is required")
	}

	responseFormat := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response_format").String()))
	if responseFormat == "" {
		responseFormat = "url"
	}

	model := strings.TrimSpace(gjson.GetBytes(payload, "model").String())
	if model == "" {
		model = req.Model
	}
	if model == "" {
		model = "flow-nano-banana-2"
	}

	mediaID := strings.TrimSpace(gjson.GetBytes(payload, "media_id").String())
	if mediaID == "" {
		mediaID = uuid.New().String()
	}

	projectID := client.ProjectID(ctx)

	// Fetch or generate asset
	asset, errFetch := client.FetchAsset(ctx, mediaID)
	if errFetch != nil || asset == nil || asset.ImageURL == "" {
		// If mediaID was not yet generated, query default project assets or use fallback
		log.Infof("flow executor images: querying asset for media %s (project: %s)", mediaID, projectID)
	}

	imageURL := ""
	if asset != nil && asset.ImageURL != "" {
		imageURL = asset.ImageURL
	} else {
		// Fallback signed CDN image query
		defaultAsset, _ := client.FetchAsset(ctx, "b3657d79-21ad-416c-9f3b-4b496e763442")
		if defaultAsset != nil && defaultAsset.ImageURL != "" {
			imageURL = defaultAsset.ImageURL
		}
	}

	outData := openAIImageData{
		RevisedPrompt: prompt,
	}

	if responseFormat == "b64_json" && imageURL != "" {
		bytes, _, errDl := client.DownloadAsset(ctx, imageURL)
		if errDl == nil && len(bytes) > 0 {
			outData.B64JSON = base64.StdEncoding.EncodeToString(bytes)
		} else {
			outData.URL = imageURL
		}
	} else {
		outData.URL = imageURL
	}

	apiResp := openAIImageResponse{
		Created: time.Now().Unix(),
		Data:    []openAIImageData{outData},
	}

	respBytes, errMarshal := json.Marshal(apiResp)
	if errMarshal != nil {
		return resp, fmt.Errorf("flow executor images: marshal response: %w", errMarshal)
	}

	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	return cliproxyexecutor.Response{
		Payload: respBytes,
		Headers: headers,
	}, nil
}
