package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	kiroauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/kiro"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	log "github.com/sirupsen/logrus"
)

const (
	// kiroAgentMode selects the Kiro agent behaviour for generateAssistantResponse.
	kiroAgentMode = "vibe"
	// kiroClientVersion mirrors the Kiro IDE client string the runtime expects.
	kiroClientVersion = "aws-sdk-js/1.0.39 KiroIDE-0.12.333"
	// kiroDefaultRegion is used when the session carries no region.
	kiroDefaultRegion = "us-east-1"
)

// kiroConversations caches conversation IDs per credential and model so Kiro can keep
// context cached per thread across turns.
var kiroConversations sync.Map

// KiroExecutor talks to the Kiro runtime (AWS CodeWhisperer) generateAssistantResponse
// endpoint. Requests are translated to OpenAI chat completions first, then converted
// into Kiro's proprietary body; responses arrive as an AWS event-stream and are
// converted back into OpenAI SSE before downstream translation.
type KiroExecutor struct {
	cfg *config.Config
}

// NewKiroExecutor creates a new Kiro executor.
func NewKiroExecutor(cfg *config.Config) *KiroExecutor {
	return &KiroExecutor{cfg: cfg}
}

// Identifier returns the provider identifier.
func (e *KiroExecutor) Identifier() string { return "kiro" }

// RequestToFormat reports the upstream request format used after auth selection.
// Kiro has no public protocol of its own, so requests are normalized to OpenAI chat
// completions and converted inside this executor.
func (e *KiroExecutor) RequestToFormat(_ cliproxyexecutor.Request, _ cliproxyexecutor.Options) sdktranslator.Format {
	return sdktranslator.FormatOpenAI
}

// PrepareRequest injects Kiro credentials into the outgoing HTTP request.
func (e *KiroExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	creds := kiroCreds(auth)
	if strings.TrimSpace(creds.accessToken) != "" {
		req.Header.Set("Authorization", "Bearer "+creds.accessToken)
	} else {
		req.Header.Del("Authorization")
	}
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(req, attrs)
	return nil
}

// HttpRequest injects Kiro credentials into the request and executes it.
func (e *KiroExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("kiro executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if errPrepare := e.PrepareRequest(httpReq, auth); errPrepare != nil {
		return nil, errPrepare
	}
	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

// kiroPreparedRequest holds everything needed to issue a Kiro runtime call.
type kiroPreparedRequest struct {
	from            sdktranslator.Format
	to              sdktranslator.Format
	responseFormat  sdktranslator.Format
	baseModel       string
	upstreamModel   string
	originalPayload []byte
	openAIBody      []byte
	kiroBody        []byte
	url             string
}

// prepare translates the inbound payload and builds the Kiro runtime body.
func (e *KiroExecutor) prepare(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, stream bool) (*kiroPreparedRequest, error) {
	from := opts.SourceFormat
	to := sdktranslator.FromString("openai")
	responseFormat := cliproxyexecutor.ResponseFormatOrSource(opts)
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	originalPayloadSource := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayloadSource = opts.OriginalRequest
	}
	originalPayload := bytes.Clone(originalPayloadSource)
	originalTranslated := helps.TranslateRequestWithCodexMultiAgentV2(ctx, opts.Headers, e.cfg, from, to, baseModel, originalPayload, stream)
	openAIBody := helps.TranslateRequestWithCodexMultiAgentV2(ctx, opts.Headers, e.cfg, from, to, baseModel, bytes.Clone(req.Payload), stream)

	openAIBody, err := helps.ApplyThinkingWithSourcePayload(openAIBody, req.Payload, originalPayloadSource, req.Model, from.String(), "openai", e.Identifier())
	if err != nil {
		return nil, err
	}

	requestedModel := helps.PayloadRequestedModel(opts, req.Model)
	requestPath := helps.PayloadRequestPath(opts)
	openAIBody = helps.ApplyPayloadConfigWithRequest(e.cfg, baseModel, to.String(), from.String(), "", openAIBody, originalTranslated, requestedModel, requestPath, opts.Headers)

	creds := kiroCreds(auth)
	upstreamModel := normalizeKiroUpstreamModel(baseModel)
	kiroBody, err := helps.BuildKiroRequest(helps.KiroRequestInput{
		Model:          upstreamModel,
		Payload:        openAIBody,
		ProfileArn:     creds.profileArn,
		ConversationID: kiroConversationID(auth, upstreamModel),
	})
	if err != nil {
		return nil, fmt.Errorf("kiro executor: failed to build request: %w", err)
	}

	return &kiroPreparedRequest{
		from:            from,
		to:              to,
		responseFormat:  responseFormat,
		baseModel:       baseModel,
		upstreamModel:   upstreamModel,
		originalPayload: originalPayload,
		openAIBody:      openAIBody,
		kiroBody:        kiroBody,
		url:             kiroRuntimeEndpoint(creds.region),
	}, nil
}

// send issues the Kiro runtime call, refreshing the credential once on a 403 because a
// mid-session rejection usually means the cached bearer expired between turns.
func (e *KiroExecutor) send(ctx context.Context, auth *cliproxyauth.Auth, prepared *kiroPreparedRequest, reporter *helps.UsageReporter) (*http.Response, error) {
	httpResp, err := e.doSend(ctx, auth, prepared, reporter)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode != http.StatusForbidden {
		return httpResp, nil
	}

	if errClose := httpResp.Body.Close(); errClose != nil {
		log.Errorf("kiro executor: close response body error: %v", errClose)
	}
	refreshed, errRefresh := e.Refresh(ctx, auth)
	if errRefresh != nil {
		return nil, errRefresh
	}
	if refreshed != nil {
		auth = refreshed
	}
	creds := kiroCreds(auth)
	prepared.url = kiroRuntimeEndpoint(creds.region)
	return e.doSend(ctx, auth, prepared, reporter)
}

// doSend performs a single Kiro runtime call.
func (e *KiroExecutor) doSend(ctx context.Context, auth *cliproxyauth.Auth, prepared *kiroPreparedRequest, reporter *helps.UsageReporter) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, prepared.url, bytes.NewReader(prepared.kiroBody))
	if err != nil {
		return nil, err
	}
	applyKiroHeaders(httpReq, auth)
	var attrs map[string]string
	var authID, authLabel, authType, authValue string
	if auth != nil {
		attrs = auth.Attributes
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       prepared.url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      prepared.kiroBody,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	if reporter != nil {
		httpClient = reporter.TrackHTTPClient(httpClient)
	}
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	return httpResp, nil
}

// Execute performs a non-streaming Kiro request.
func (e *KiroExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	prepared, err := e.prepare(ctx, auth, req, opts, false)
	if err != nil {
		return resp, err
	}

	reporter := helps.NewExecutorUsageReporter(ctx, e, prepared.baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	httpResp, err := e.send(ctx, auth, prepared, reporter)
	if err != nil {
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("kiro executor: close response body error: %v", errClose)
		}
	}()

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	helps.AppendAPIResponseChunk(ctx, e.cfg, data)
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), data))
		err = kiroStatusErr(httpResp.StatusCode, data)
		return resp, err
	}

	collected := helps.CollectKiroResponse(prepared.upstreamModel, data)
	completion, err := helps.BuildKiroChatCompletion("chatcmpl-"+uuid.NewString(), req.Model, time.Now().Unix(), collected)
	if err != nil {
		return resp, fmt.Errorf("kiro executor: failed to build completion: %w", err)
	}

	var param any
	out := sdktranslator.TranslateNonStream(ctx, prepared.to, prepared.responseFormat, req.Model, opts.OriginalRequest, prepared.openAIBody, completion, &param)
	if prepared.responseFormat == sdktranslator.FormatOpenAIResponse {
		out = helps.EnsureResponsesUsageDetails(out)
	}
	resp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}
	return resp, nil
}

// ExecuteStream performs a streaming Kiro request.
func (e *KiroExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	prepared, err := e.prepare(ctx, auth, req, opts, true)
	if err != nil {
		return nil, err
	}

	reporter := helps.NewExecutorUsageReporter(ctx, e, prepared.baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	httpResp, err := e.send(ctx, auth, prepared, reporter)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		data, errRead := io.ReadAll(httpResp.Body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("kiro executor: close response body error: %v", errClose)
		}
		if errRead != nil {
			helps.RecordAPIResponseError(ctx, e.cfg, errRead)
			return nil, errRead
		}
		helps.AppendAPIResponseChunk(ctx, e.cfg, data)
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), data))
		err = kiroStatusErr(httpResp.StatusCode, data)
		return nil, err
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("kiro executor: close response body error: %v", errClose)
			}
		}()

		decoder := &helps.KiroEventStreamDecoder{}
		converter := helps.NewKiroResponseConverter(prepared.upstreamModel)
		claudeInputTokens := helps.NewClaudeInputTokenState(prepared.from, prepared.to, prepared.responseFormat, prepared.originalPayload)
		var param any

		emit := func(lines [][]byte) bool {
			for _, line := range lines {
				chunks := helps.TranslateStreamWithClaudeInputTokens(ctx, prepared.to, prepared.responseFormat, req.Model, opts.OriginalRequest, prepared.openAIBody, line, &param, claudeInputTokens)
				for i := range chunks {
					select {
					case out <- cliproxyexecutor.StreamChunk{Payload: chunks[i]}:
					case <-ctx.Done():
						return false
					}
				}
			}
			return true
		}

		buf := make([]byte, 32*1024)
		for {
			n, errRead := httpResp.Body.Read(buf)
			if n > 0 {
				helps.AppendAPIResponseChunk(ctx, e.cfg, buf[:n])
				var lines [][]byte
				for _, event := range decoder.Push(bytes.Clone(buf[:n])) {
					lines = append(lines, converter.Handle(event)...)
				}
				if len(lines) > 0 && !emit(lines) {
					return
				}
			}
			if errRead != nil {
				if errRead == io.EOF {
					break
				}
				helps.RecordAPIResponseError(ctx, e.cfg, errRead)
				reporter.PublishFailure(ctx, errRead)
				select {
				case out <- cliproxyexecutor.StreamChunk{Err: errRead}:
				case <-ctx.Done():
				}
				return
			}
		}
		emit(converter.Finish())
	}()
	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}, nil
}

// CountTokens estimates the token count for a Kiro request. The Kiro runtime exposes no
// token counting endpoint, so the translated OpenAI payload is measured locally.
func (e *KiroExecutor) CountTokens(ctx context.Context, _ *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName
	from := opts.SourceFormat
	to := sdktranslator.FromString("openai")
	responseFormat := cliproxyexecutor.ResponseFormatOrSource(opts)

	translated := helps.TranslateRequestWithCodexMultiAgentV2(ctx, opts.Headers, e.cfg, from, to, baseModel, bytes.Clone(req.Payload), false)

	enc, err := helps.TokenizerForModel(baseModel)
	if err != nil {
		return cliproxyexecutor.Response{}, fmt.Errorf("kiro executor: tokenizer init failed: %w", err)
	}
	count, err := helps.CountOpenAIChatTokens(enc, translated)
	if err != nil {
		return cliproxyexecutor.Response{}, fmt.Errorf("kiro executor: token counting failed: %w", err)
	}

	usageJSON := helps.BuildOpenAIUsageJSON(count)
	out := sdktranslator.TranslateTokenCount(ctx, to, responseFormat, count, usageJSON)
	return cliproxyexecutor.Response{Payload: out}, nil
}

// Refresh exchanges the Kiro refresh token for a new access token.
func (e *KiroExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	log.Debugf("kiro executor: refresh called")
	if refreshed, handled, err := helps.RefreshAuthViaHome(ctx, e.cfg, auth); handled {
		return refreshed, err
	}
	if auth == nil {
		return nil, fmt.Errorf("kiro executor: auth is nil")
	}

	creds := kiroCreds(auth)
	if strings.TrimSpace(creds.refreshToken) == "" {
		return auth, nil
	}

	session := &kiroauth.KiroAuth{
		AccessToken:  creds.accessToken,
		RefreshToken: creds.refreshToken,
		ProfileArn:   creds.profileArn,
		Region:       creds.region,
		AuthMethod:   creds.authMethod,
	}
	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 30*time.Second)
	next, err := kiroauth.RefreshKiroAuth(session, httpClient)
	if err != nil {
		return nil, err
	}

	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["type"] = "kiro"
	auth.Metadata["access_token"] = next.AccessToken
	if next.RefreshToken != "" {
		auth.Metadata["refresh_token"] = next.RefreshToken
	}
	if next.ProfileArn != "" {
		auth.Metadata["profile_arn"] = next.ProfileArn
	}
	if next.Region != "" {
		auth.Metadata["region"] = next.Region
	}
	if next.AuthMethod != "" {
		auth.Metadata["auth_method"] = next.AuthMethod
	}
	if next.ExpiresAt > 0 {
		auth.Metadata["expired"] = time.UnixMilli(next.ExpiresAt).UTC().Format(time.RFC3339)
	}
	auth.Metadata["last_refresh"] = time.Now().Format(time.RFC3339)
	return auth, nil
}

// kiroCredentials holds the session values needed to call the Kiro runtime.
type kiroCredentials struct {
	accessToken  string
	refreshToken string
	profileArn   string
	region       string
	authMethod   string
}

// kiroCreds extracts Kiro session values from an auth record.
func kiroCreds(a *cliproxyauth.Auth) kiroCredentials {
	creds := kiroCredentials{}
	if a == nil {
		creds.region = kiroDefaultRegion
		return creds
	}
	read := func(key string) string {
		if a.Metadata != nil {
			if v, ok := a.Metadata[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		if a.Attributes != nil {
			if v := strings.TrimSpace(a.Attributes[key]); v != "" {
				return v
			}
		}
		return ""
	}
	creds.accessToken = read("access_token")
	creds.refreshToken = read("refresh_token")
	creds.profileArn = read("profile_arn")
	creds.region = read("region")
	creds.authMethod = read("auth_method")
	if creds.region == "" {
		creds.region = kiroDefaultRegion
	}
	if creds.authMethod == "" {
		creds.authMethod = "social"
	}
	return creds
}

// kiroRuntimeEndpoint builds the regional generateAssistantResponse URL.
func kiroRuntimeEndpoint(region string) string {
	if strings.TrimSpace(region) == "" {
		region = kiroDefaultRegion
	}
	return fmt.Sprintf("https://runtime.%s.kiro.dev/generateAssistantResponse", region)
}

// applyKiroHeaders sets the headers the Kiro runtime expects.
func applyKiroHeaders(r *http.Request, auth *cliproxyauth.Auth) {
	creds := kiroCreds(auth)
	r.Header.Set("Content-Type", "application/json")
	if creds.accessToken != "" {
		r.Header.Set("Authorization", "Bearer "+creds.accessToken)
	}
	r.Header.Set("Accept", "application/vnd.amazon.eventstream, application/json, */*")
	r.Header.Set("x-amzn-kiro-agent-mode", kiroAgentMode)
	r.Header.Set("x-amz-user-agent", kiroClientVersion)
	r.Header.Set("amz-sdk-invocation-id", uuid.NewString())
	r.Header.Set("amz-sdk-request", "attempt=1; max=3")
}

// kiroConversationID returns a stable conversation ID per credential and model.
func kiroConversationID(auth *cliproxyauth.Auth, model string) string {
	authID := ""
	if auth != nil {
		authID = auth.ID
	}
	key := authID + "|" + model
	if existing, ok := kiroConversations.Load(key); ok {
		if id, okStr := existing.(string); okStr && id != "" {
			return id
		}
	}
	id := uuid.NewString()
	actual, _ := kiroConversations.LoadOrStore(key, id)
	if stored, ok := actual.(string); ok && stored != "" {
		return stored
	}
	return id
}

// normalizeKiroUpstreamModel maps a CLIProxyAPI model ID onto the Kiro model ID.
// The "kiro-" prefix is optional in client requests and stripped here.
func normalizeKiroUpstreamModel(model string) string {
	model = strings.TrimSpace(thinking.ParseSuffix(model).ModelName)
	if model == "" {
		return "auto"
	}
	lower := strings.ToLower(model)
	if strings.HasPrefix(lower, "kiro-") {
		trimmed := model[len("kiro-"):]
		if strings.TrimSpace(trimmed) != "" {
			return trimmed
		}
	}
	return model
}

// kiroStatusErr maps a Kiro runtime failure onto a status-carrying error.
// A 402 means the credit allowance is exhausted, so retrying is pointless; 429 and 5xx
// clear on their own and stay retryable through the generic status handling.
func kiroStatusErr(status int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	if len(detail) > 400 {
		detail = detail[:400]
	}
	var label string
	switch status {
	case http.StatusPaymentRequired:
		label = "Kiro credit limit reached"
	case http.StatusForbidden:
		label = "Kiro rejected the session (sign in again in the Kiro app)"
	case http.StatusTooManyRequests:
		label = "Kiro rate limit"
	default:
		label = fmt.Sprintf("Kiro request failed (%d)", status)
	}
	return statusErr{code: status, msg: fmt.Sprintf("%s: %s", label, detail)}
}
