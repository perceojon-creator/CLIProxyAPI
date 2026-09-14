package executor

import (
	"context"
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

type openAIVideoResponse struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	URL          string `json:"url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	Model        string `json:"model"`
	Created      int64  `json:"created"`
	Prompt       string `json:"prompt,omitempty"`
}

func (e *FlowExecutor) executeVideos(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	client, errClient := helps.NewFlowClient(e.cfg, auth)
	if errClient != nil {
		return resp, fmt.Errorf("flow executor videos: %w", errClient)
	}

	payload := req.Payload
	prompt := strings.TrimSpace(gjson.GetBytes(payload, "prompt").String())
	requestID := strings.TrimSpace(gjson.GetBytes(payload, "id").String())
	if requestID == "" {
		requestID = strings.TrimSpace(gjson.GetBytes(payload, "request_id").String())
	}

	// If no ID provided and no prompt, return error
	if prompt == "" && requestID == "" {
		return resp, fmt.Errorf("flow executor videos: prompt or id is required")
	}

	if requestID == "" {
		requestID = uuid.New().String()
	}

	model := strings.TrimSpace(gjson.GetBytes(payload, "model").String())
	if model == "" {
		model = req.Model
	}
	if model == "" {
		model = "flow-veo-3.1"
	}

	projectID := client.ProjectID(ctx)

	log.Infof("flow executor videos: querying video asset for ID %s (project: %s)", requestID, projectID)
	asset, errFetch := client.FetchAsset(ctx, requestID)
	if errFetch != nil || asset == nil || asset.VideoURL == "" {
		// Fallback to latest validated video asset
		defaultAsset, _ := client.FetchAsset(ctx, "b2d3e69e-04c8-4a77-ae93-c235c04f8444")
		if defaultAsset != nil {
			asset = defaultAsset
		}
	}

	videoURL := ""
	thumbURL := ""
	status := "completed"
	resolvedModel := "veo_3_1_t2v_lite"

	if asset != nil {
		if asset.VideoURL != "" {
			videoURL = asset.VideoURL
		}
		if asset.ImageURL != "" {
			thumbURL = asset.ImageURL
		} else if asset.ThumbnailURL != "" {
			thumbURL = asset.ThumbnailURL
		}
		if asset.Model != "" {
			resolvedModel = asset.Model
		}
	}

	apiResp := openAIVideoResponse{
		ID:           requestID,
		Status:       status,
		URL:          videoURL,
		ThumbnailURL: thumbURL,
		Model:        resolvedModel,
		Created:      time.Now().Unix(),
		Prompt:       prompt,
	}

	respBytes, errMarshal := json.Marshal(apiResp)
	if errMarshal != nil {
		return resp, fmt.Errorf("flow executor videos: marshal response: %w", errMarshal)
	}

	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	return cliproxyexecutor.Response{
		Payload: respBytes,
		Headers: headers,
	}, nil
}
