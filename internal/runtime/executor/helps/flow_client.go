package helps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/flow"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

var (
	reAtToken   = regexp.MustCompile(`"SNlM0e":"([^"]+)"`)
	reFSid      = regexp.MustCompile(`"FdrFJe":"([^"]+)"`)
	reBuildLab  = regexp.MustCompile(`"cfb2h":"([^"]+)"`)
	reProjectID = regexp.MustCompile(`"tools/PINHOLE/projects/([a-zA-Z0-9_-]+)"`)
)

var flowReqCounter uint64

// FlowClient handles API interactions with Google Flow endpoints.
type FlowClient struct {
	cfg         *config.Config
	auth        *cliproxyauth.Auth
	flowAuth    *flow.FlowAuth
	mu          sync.RWMutex
	atToken     string
	fSid        string
	buildLabel  string
	projectID   string
	tokenExpiry time.Time
}

// FlowAssetInfo describes a resolved Google Flow media asset.
type FlowAssetInfo struct {
	MediaID      string
	ProjectID    string
	Prompt       string
	Model        string
	ImageURL     string
	VideoURL     string
	ThumbnailURL string
	Status       string
	RawJSON      string
}

// NewFlowClient creates a new FlowClient bound to the provided credentials.
func NewFlowClient(cfg *config.Config, auth *cliproxyauth.Auth) (*FlowClient, error) {
	if auth == nil {
		return nil, fmt.Errorf("flow client: auth is nil")
	}

	var flowAuth *flow.FlowAuth
	if auth.Storage != nil {
		if ts, ok := auth.Storage.(*flow.FlowTokenStorage); ok {
			flowAuth = &flow.FlowAuth{
				SessionToken: ts.SessionToken,
				CSRFToken:    ts.CSRFToken,
				Cookies:      ts.Cookies,
				AtToken:      ts.AtToken,
				ProjectID:    ts.ProjectID,
				Email:        ts.Email,
				Name:         ts.Name,
				ProfileDir:   ts.ProfileDir,
			}
		}
	}

	if flowAuth == nil && auth.Metadata != nil {
		flowAuth = &flow.FlowAuth{
			SessionToken: fmt.Sprint(auth.Metadata["session_token"]),
			CSRFToken:    fmt.Sprint(auth.Metadata["csrf_token"]),
			Cookies:      fmt.Sprint(auth.Metadata["cookies"]),
			AtToken:      fmt.Sprint(auth.Metadata["at_token"]),
			ProjectID:    fmt.Sprint(auth.Metadata["project_id"]),
			Email:        fmt.Sprint(auth.Metadata["email"]),
			Name:         fmt.Sprint(auth.Metadata["name"]),
			ProfileDir:   fmt.Sprint(auth.Metadata["profile_dir"]),
		}
	}

	if flowAuth == nil {
		return nil, fmt.Errorf("flow client: could not resolve flow auth from record")
	}

	client := &FlowClient{
		cfg:       cfg,
		auth:      auth,
		flowAuth:  flowAuth,
		atToken:   flowAuth.AtToken,
		projectID: flowAuth.ProjectID,
	}

	return client, nil
}

// EnsureTokens fetches or refreshes the WIZ tokens (at, f.sid, build label) from flow.google.com.
func (c *FlowClient) EnsureTokens(ctx context.Context) (at, fSid, bl string, err error) {
	c.mu.RLock()
	if c.atToken != "" && c.fSid != "" && c.buildLabel != "" && time.Now().Before(c.tokenExpiry) {
		at = c.atToken
		fSid = c.fSid
		bl = c.buildLabel
		c.mu.RUnlock()
		return at, fSid, bl, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.atToken != "" && c.fSid != "" && c.buildLabel != "" && time.Now().Before(c.tokenExpiry) {
		return c.atToken, c.fSid, c.buildLabel, nil
	}

	httpClient := NewProxyAwareHTTPClient(ctx, c.cfg, c.auth, 30*time.Second)
	req, errReq := http.NewRequestWithContext(ctx, http.MethodGet, "https://flow.google.com/", nil)
	if errReq != nil {
		return "", "", "", fmt.Errorf("flow client: create page request: %w", errReq)
	}

	cookieHeader := c.flowAuth.CookieHeader()
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, errDo := httpClient.Do(req)
	if errDo != nil {
		return "", "", "", fmt.Errorf("flow client: get page: %w", errDo)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("flow client: close page response body: %v", errClose)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("flow client: get page status %d", resp.StatusCode)
	}

	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return "", "", "", fmt.Errorf("flow client: read page: %w", errRead)
	}
	bodyStr := string(body)

	mAt := reAtToken.FindStringSubmatch(bodyStr)
	if len(mAt) > 1 {
		c.atToken = mAt[1]
	}
	mFSid := reFSid.FindStringSubmatch(bodyStr)
	if len(mFSid) > 1 {
		c.fSid = mFSid[1]
	}
	mBL := reBuildLab.FindStringSubmatch(bodyStr)
	if len(mBL) > 1 {
		c.buildLabel = mBL[1]
	}

	if c.projectID == "" {
		mProj := reProjectID.FindStringSubmatch(bodyStr)
		if len(mProj) > 1 {
			c.projectID = mProj[1]
		}
	}

	if c.buildLabel == "" {
		c.buildLabel = "boq_labs-ai-sandbox-frontend_20260909.10_p0"
	}

	if c.atToken == "" {
		return "", "", "", fmt.Errorf("flow client: unable to extract at token from flow.google.com page")
	}

	c.tokenExpiry = time.Now().Add(10 * time.Minute)
	return c.atToken, c.fSid, c.buildLabel, nil
}

// ProjectID returns the active project UUID.
func (c *FlowClient) ProjectID(ctx context.Context) string {
	c.mu.RLock()
	if c.projectID != "" {
		pid := c.projectID
		c.mu.RUnlock()
		return pid
	}
	c.mu.RUnlock()

	_, _, _, _ = c.EnsureTokens(ctx)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.projectID != "" {
		return c.projectID
	}
	return "9dd588d0-405b-4fb9-95f7-063bd0ab8f33" // Default fallback project
}

// DoBatchExecute issues an RPC call via batchexecute.
func (c *FlowClient) DoBatchExecute(ctx context.Context, rpcID, innerPayload, sourcePath string) (string, error) {
	at, fSid, bl, errTokens := c.EnsureTokens(ctx)
	if errTokens != nil {
		return "", errTokens
	}

	reqID := atomic.AddUint64(&flowReqCounter, 100000)
	query := url.Values{}
	query.Set("rpcids", rpcID)
	if sourcePath != "" {
		query.Set("source-path", sourcePath)
	}
	query.Set("bl", bl)
	if fSid != "" {
		query.Set("f.sid", fSid)
	}
	query.Set("hl", "es")
	query.Set("_reqid", strconv.FormatUint(reqID, 10))
	query.Set("rt", "c")

	postURL := "https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute?" + query.Encode()

	freqVal := fmt.Sprintf(`[[["%s",%s,null,"generic"]]]`, rpcID, strconv.Quote(innerPayload))
	data := url.Values{}
	data.Set("f.req", freqVal)
	data.Set("at", at)

	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(data.Encode()))
	if errReq != nil {
		return "", fmt.Errorf("flow client: create batch request: %w", errReq)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("Origin", "https://flow.google.com")
	req.Header.Set("Referer", "https://flow.google.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")
	req.Header.Set("X-Same-Domain", "1")
	cookieHeader := c.flowAuth.CookieHeader()
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	httpClient := NewProxyAwareHTTPClient(ctx, c.cfg, c.auth, 0)
	resp, errDo := httpClient.Do(req)
	if errDo != nil {
		return "", fmt.Errorf("flow client: post batch execute: %w", errDo)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("flow client: close batch response: %v", errClose)
		}
	}()

	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return "", fmt.Errorf("flow client: read batch execute: %w", errRead)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("flow client: batchexecute returned status %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}

// FetchAsset queries as29s for a media item and parses its signed CDN URLs.
func (c *FlowClient) FetchAsset(ctx context.Context, mediaID string) (*FlowAssetInfo, error) {
	inner := fmt.Sprintf(`["%s"]`, mediaID)
	raw, err := c.DoBatchExecute(ctx, "as29s", inner, "")
	if err != nil {
		return nil, fmt.Errorf("fetch asset: %w", err)
	}

	info := &FlowAssetInfo{
		MediaID: mediaID,
		RawJSON: raw,
	}

	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[[") {
			continue
		}
		var parsed []any
		if errJSON := json.Unmarshal([]byte(trimmed), &parsed); errJSON == nil {
			for _, item := range parsed {
				itemArr, ok := item.([]any)
				if !ok || len(itemArr) < 3 {
					continue
				}
				if itemArr[0] == "wrb.fr" && itemArr[1] == "as29s" {
					innerStr, okInner := itemArr[2].(string)
					if okInner && innerStr != "" {
						c.parseAssetInner(innerStr, info)
						return info, nil
					}
				}
			}
		}
	}

	return info, nil
}

func (c *FlowClient) parseAssetInner(innerStr string, info *FlowAssetInfo) {
	var innerData []any
	if err := json.Unmarshal([]byte(innerStr), &innerData); err != nil {
		return
	}
	if len(innerData) > 1 && innerData[1] != nil {
		info.ProjectID = fmt.Sprint(innerData[1])
	}
	// Look for URLs starting with https://flow-content.google
	urls := regexp.MustCompile(`https://flow-content\.google/(image|video)/[^"\\\s]+`).FindAllString(innerStr, -1)
	for _, u := range urls {
		cleanURL := strings.ReplaceAll(u, `\u003d`, "=")
		cleanURL = strings.ReplaceAll(cleanURL, `\u0026`, "&")
		if strings.Contains(cleanURL, "/video/") {
			info.VideoURL = cleanURL
		} else if strings.Contains(cleanURL, "/image/") {
			if info.ImageURL == "" {
				info.ImageURL = cleanURL
			} else {
				info.ThumbnailURL = cleanURL
			}
		}
	}

	promptMatch := regexp.MustCompile(`<prompt>(.*?)</prompt>`).FindStringSubmatch(innerStr)
	if len(promptMatch) > 1 {
		info.Prompt = promptMatch[1]
	}

	if strings.Contains(innerStr, "veo_3_1") {
		info.Model = "veo_3_1_t2v_lite"
	} else if strings.Contains(innerStr, "NARWHAL") || strings.Contains(innerStr, "narwhal") {
		info.Model = "narwhal_display"
	}
}

// DownloadAsset retrieves the raw bytes of an image or video from flow-content.google.
func (c *FlowClient) DownloadAsset(ctx context.Context, cdnURL string) ([]byte, string, error) {
	httpClient := NewProxyAwareHTTPClient(ctx, c.cfg, c.auth, 0)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cdnURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://flow.google.com/")

	resp, errDo := httpClient.Do(req)
	if errDo != nil {
		return nil, "", fmt.Errorf("download asset: %w", errDo)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("flow client: close download response: %v", errClose)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download asset status: %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	data, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return nil, "", fmt.Errorf("read asset bytes: %w", errRead)
	}

	return data, contentType, nil
}
