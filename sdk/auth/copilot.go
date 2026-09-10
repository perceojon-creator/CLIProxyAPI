package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	copilotauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/copilot"
	kiroauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/kiro"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// CopilotAuthenticator implements Authenticator for GitHub Copilot subscription.
type CopilotAuthenticator struct{}

// NewCopilotAuthenticator constructs a new Copilot authenticator instance.
func NewCopilotAuthenticator() Authenticator { return &CopilotAuthenticator{} }

// Provider returns "copilot".
func (CopilotAuthenticator) Provider() string { return "copilot" }

// RefreshLead instructs the manager to refresh 30 minutes before token expiry.
func (CopilotAuthenticator) RefreshLead() *time.Duration {
	lead := 30 * time.Minute
	return &lead
}

// Login obtains Copilot credentials via local detection, browser Device Flow, loopback OAuth, or manual prompt.
func (CopilotAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts == nil {
		opts = &LoginOptions{}
	}

	authSvc := copilotauth.NewCopilotAuth(cfg, nil)

	// 1. Try detecting active local credentials on the host (Windows Credential Manager, gh, env)
	if !opts.ForceBrowser {
		if localToken, localUser, localSKU, localBaseURL, errLocal := copilotauth.DetectLocalCredentials(ctx, cfg); errLocal == nil && localToken != "" {
			log.Infof("copilot: using active local credentials for %s (SKU: %s)", localUser, localSKU)
			fmt.Printf("Detected active GitHub Copilot credentials for %s (SKU: %s)\n", localUser, localSKU)
			return BuildCopilotAuth(localToken, "", localUser, "", localSKU, localBaseURL, 0), nil
		}
	}

	// 2. Official GitHub Copilot Device Code Flow (the standard protocol for Copilot CLI subscription)
	dcr, errDev := authSvc.StartDeviceFlow(ctx)
	if errDev == nil && dcr != nil {
		fmt.Printf("\n=== GitHub Copilot Device Authentication ===\n")
		fmt.Printf("Please visit: %s\n", dcr.VerificationURI)
		fmt.Printf("Enter code:   %s\n\n", dcr.UserCode)

		if !opts.NoBrowser {
			fmt.Println("Opening browser for authorization...")
			profileDir := ""
			if opts.Metadata != nil {
				profileDir = opts.Metadata["profile_dir"]
			}
			if profileDir != "" {
				if errChrome := kiroauth.OpenURLInChromeProfile(dcr.VerificationURI, profileDir); errChrome != nil {
					log.Warnf("Failed to open Chrome profile %s: %v, falling back to default browser", profileDir, errChrome)
					_ = browser.OpenURL(dcr.VerificationURI)
				} else {
					fmt.Printf("Opened Chrome with profile: %s\n", profileDir)
				}
			} else if browser.IsAvailable() {
				if errOpen := browser.OpenURL(dcr.VerificationURI); errOpen != nil {
					log.Warnf("Failed to open browser automatically: %v", errOpen)
				}
			}
		}

		fmt.Println("Waiting for authorization from GitHub...")
		tokenResp, errWait := authSvc.WaitForDeviceAuthorization(ctx, dcr)
		if errWait == nil && tokenResp != nil && tokenResp.AccessToken != "" {
			return processCopilotTokens(ctx, authSvc, tokenResp.AccessToken)
		}
		if errWait != nil {
			log.Warnf("copilot: device code flow failed: %v, falling back to loopback flow", errWait)
		}
	}

	// 3. Fallback: Interactive Browser / Loopback flow with PKCE
	callbackPort := copilotauth.CallbackPort
	if opts.CallbackPort > 0 {
		callbackPort = opts.CallbackPort
	}

	state, errState := misc.GenerateRandomState()
	if errState != nil {
		return nil, fmt.Errorf("copilot: failed to generate state: %w", errState)
	}

	verifier, challenge, errPKCE := copilotauth.GeneratePKCE()
	if errPKCE != nil {
		return nil, fmt.Errorf("copilot: failed to generate PKCE: %w", errPKCE)
	}

	srv, port, cbChan, errServer := startCopilotCallbackServer(callbackPort)
	if errServer != nil {
		return nil, fmt.Errorf("copilot: failed to start callback server: %w", errServer)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	redirectURI := fmt.Sprintf("http://localhost:%d/oauth-callback", port)
	authURL := authSvc.BuildAuthURL(state, redirectURI, challenge)

	if !opts.NoBrowser {
		fmt.Println("Opening browser for GitHub Copilot authentication...")
		if !browser.IsAvailable() {
			log.Warn("No browser available; please open the URL manually")
			util.PrintSSHTunnelInstructions(port)
			fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
		} else if errOpen := browser.OpenURL(authURL); errOpen != nil {
			log.Warnf("Failed to open browser automatically: %v", errOpen)
			util.PrintSSHTunnelInstructions(port)
			fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
		}
	} else {
		util.PrintSSHTunnelInstructions(port)
		fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
	}

	fmt.Println("Waiting for GitHub Copilot authentication callback...")

	var cbRes copilotCallbackResult
	timeoutTimer := time.NewTimer(copilotauth.OAuthTimeout)
	defer timeoutTimer.Stop()

	var manualPromptTimer *time.Timer
	var manualPromptC <-chan time.Time
	if opts.Prompt != nil {
		manualPromptTimer = time.NewTimer(copilotauth.ManualPromptTimeout)
		manualPromptC = manualPromptTimer.C
		defer manualPromptTimer.Stop()
	}

	var manualInputCh <-chan string
	var manualInputErrCh <-chan error

waitForCallback:
	for {
		select {
		case res := <-cbChan:
			cbRes = res
			break waitForCallback
		case <-manualPromptC:
			manualPromptC = nil
			if manualPromptTimer != nil {
				manualPromptTimer.Stop()
			}
			select {
			case res := <-cbChan:
				cbRes = res
				break waitForCallback
			default:
			}
			manualInputCh, manualInputErrCh = misc.AsyncPrompt(opts.Prompt, "Paste the GitHub Copilot callback URL (or press Enter to keep waiting): ")
			continue
		case input := <-manualInputCh:
			manualInputCh = nil
			manualInputErrCh = nil
			parsed, errParse := misc.ParseOAuthCallback(input)
			if errParse != nil {
				return nil, errParse
			}
			if parsed == nil {
				continue
			}
			cbRes = copilotCallbackResult{
				Code:  parsed.Code,
				State: parsed.State,
				Error: parsed.Error,
			}
			break waitForCallback
		case errManual := <-manualInputErrCh:
			return nil, errManual
		case <-timeoutTimer.C:
			return nil, fmt.Errorf("copilot: authentication timed out")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if cbRes.Error != "" {
		return nil, fmt.Errorf("copilot: authentication failed: %s", cbRes.Error)
	}
	if cbRes.State != state {
		return nil, fmt.Errorf("copilot: invalid state parameter")
	}
	if cbRes.Code == "" {
		return nil, fmt.Errorf("copilot: missing authorization code")
	}

	// Exchange code for tokens
	tokenResp, errToken := authSvc.ExchangeCodeForTokens(ctx, cbRes.Code, redirectURI, verifier)
	if errToken != nil {
		return nil, fmt.Errorf("copilot: token exchange failed: %w", errToken)
	}
	accessToken := strings.TrimSpace(tokenResp.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("copilot: empty access token returned")
	}

	return processCopilotTokens(ctx, authSvc, accessToken)
}

func processCopilotTokens(ctx context.Context, authSvc *copilotauth.CopilotAuth, accessToken string) (*coreauth.Auth, error) {
	// Fetch user profile and Copilot subscription details
	user := ""
	email := ""
	if profile, errProfile := authSvc.FetchUserInfo(ctx, accessToken); errProfile == nil && profile != nil {
		user = profile.Login
		email = profile.Email
	}

	sku := ""
	baseURL := copilotauth.DefaultUpstreamBaseURL
	if copilotUser, errCU := authSvc.FetchCopilotUser(ctx, accessToken); errCU == nil && copilotUser != nil {
		if user == "" {
			user = copilotUser.Login
		}
		sku = copilotUser.AccessTypeSKU
		if copilotUser.Endpoints.API != "" {
			baseURL = copilotUser.Endpoints.API
		}
	}

	copilotToken := ""
	expiresAt := int64(0)
	if session, errSess := authSvc.FetchCopilotSession(ctx, accessToken); errSess == nil && session != nil {
		copilotToken = session.Token
		expiresAt = session.ExpiresAt
		if session.AccessTypeSKU != "" && sku == "" {
			sku = session.AccessTypeSKU
		}
		if apiEndpoint, ok := session.Endpoints["api"]; ok && apiEndpoint != "" {
			baseURL = apiEndpoint
		}
	}

	fmt.Printf("GitHub Copilot authentication successful for %s (SKU: %s)\n", user, sku)
	return BuildCopilotAuth(accessToken, copilotToken, user, email, sku, baseURL, expiresAt), nil
}

type copilotCallbackResult struct {
	Code  string
	Error string
	State string
}

func startCopilotCallbackServer(port int) (*http.Server, int, <-chan copilotCallbackResult, error) {
	if port <= 0 {
		port = copilotauth.CallbackPort
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, 0, nil, err
		}
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	resultCh := make(chan copilotCallbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth-callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res := copilotCallbackResult{
			Code:  strings.TrimSpace(q.Get("code")),
			Error: strings.TrimSpace(q.Get("error")),
			State: strings.TrimSpace(q.Get("state")),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.Code != "" && res.Error == "" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><body style="font-family:sans-serif;text-align:center;padding:50px;"><h1>GitHub Copilot Login Successful</h1><p>You can close this window and return to the CLI.</p></body></html>`))
		} else {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<html><body style="font-family:sans-serif;text-align:center;padding:50px;"><h1>GitHub Copilot Login Failed</h1><p>Please check the terminal output for details.</p></body></html>`))
		}
		select {
		case resultCh <- res:
		default:
		}
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	go func() {
		if errServe := srv.Serve(listener); errServe != nil && errServe != http.ErrServerClosed {
			log.Warnf("copilot callback server error: %v", errServe)
		}
	}()

	return srv, actualPort, resultCh, nil
}

// BuildCopilotAuth creates the canonical Auth record for Copilot.
func BuildCopilotAuth(accessToken, copilotToken, user, email, sku, baseURL string, expiresAt int64) *coreauth.Auth {
	ident := strings.TrimSpace(user)
	if ident == "" {
		ident = strings.TrimSpace(email)
	}
	if ident == "" {
		ident = "copilot"
	}
	fileName := copilotauth.CredentialFileName(ident)
	if baseURL == "" {
		baseURL = copilotauth.DefaultUpstreamBaseURL
	}

	metadata := map[string]any{
		"type":         "copilot",
		"access_token": accessToken,
		"user":         user,
		"email":        email,
		"sku":          sku,
		"base_url":     baseURL,
		"timestamp":    time.Now().UnixMilli(),
	}
	attrs := map[string]string{
		"access_token":  accessToken,
		"copilot_token": copilotToken,
		"user":          user,
		"email":         email,
		"sku":           sku,
		"base_url":      baseURL,
	}
	if copilotToken != "" {
		metadata["copilot_token"] = copilotToken
	}
	if expiresAt > 0 {
		exp := time.Unix(expiresAt, 0).UTC().Format(time.RFC3339)
		metadata["expired"] = exp
		metadata["expires_at"] = expiresAt
		attrs["expired"] = exp
	}

	return &coreauth.Auth{
		ID:         fileName,
		Provider:   "copilot",
		FileName:   fileName,
		Label:      ident,
		Status:     coreauth.StatusActive,
		Metadata:   metadata,
		Attributes: attrs,
	}
}
