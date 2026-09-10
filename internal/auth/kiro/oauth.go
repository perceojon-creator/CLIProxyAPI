package kiro

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	log "github.com/sirupsen/logrus"
)

const (
	kiroAuthEndpoint = "https://prod.us-east-1.auth.desktop.kiro.dev"
	kiroOAuthTimeout = 5 * time.Minute
)

func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("kiro oauth: generate random bytes: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return verifier, challenge, nil
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("kiro oauth: generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LoginWithGoogle performs an OAuth login using the system default browser.
func LoginWithGoogle(ctx context.Context, cfg *config.Config, noBrowser bool) (*KiroAuth, error) {
	return loginWithGoogleInternal(ctx, cfg, noBrowser, nil)
}

// LoginWithGoogleProfile performs an OAuth login launching a specific Google Chrome user profile.
func LoginWithGoogleProfile(ctx context.Context, cfg *config.Config, profile ChromeProfile) (*KiroAuth, error) {
	return loginWithGoogleInternal(ctx, cfg, false, &profile)
}

func loginWithGoogleInternal(ctx context.Context, cfg *config.Config, noBrowser bool, profile *ChromeProfile) (*KiroAuth, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, err
	}
	state, err := generateState()
	if err != nil {
		return nil, err
	}

	protoHandler := NewProtocolHandler()
	_, err = protoHandler.Start(ctx)
	if err != nil {
		log.Warnf("kiro: protocol handler start failed: %v", err)
	}
	defer protoHandler.Stop()

	authURL := fmt.Sprintf("%s/login?idp=Google&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&state=%s&prompt=select_account",
		kiroAuthEndpoint,
		url.QueryEscape(KiroRedirectURI),
		challenge,
		state,
	)

	if !noBrowser {
		if profile != nil && profile.ProfileDir != "" {
			fmt.Printf("\nAbriendo Chrome con perfil '%s' (%s)...\n", profile.Name, profile.Email)
			if errOpen := OpenURLInChromeProfile(authURL, profile.ProfileDir); errOpen != nil {
				log.Warnf("No se pudo abrir Chrome con el perfil %s: %v. Intentando navegador predeterminado...", profile.ProfileDir, errOpen)
				_ = browser.OpenURL(authURL)
			}
		} else {
			fmt.Println("\nAbriendo el navegador para autenticacion con Google...")
			if errOpen := browser.OpenURL(authURL); errOpen != nil {
				log.Warnf("No se pudo abrir el navegador automaticamente: %v", errOpen)
				fmt.Printf("Por favor abre esta URL en tu navegador:\n%s\n", authURL)
			}
		}
	} else {
		fmt.Printf("Abre esta URL en tu navegador:\n%s\n", authURL)
	}

	fmt.Println("Esperando autorizacion de Google...")
	fmt.Println("(Si el navegador muestra un enlace 'kiro://...', tambien puedes copiarlo y pegarlo aqui)")

	manualChan := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			manualChan <- line
		}
	}()

	var code string
waitForAuth:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(kiroOAuthTimeout):
			return nil, fmt.Errorf("kiro oauth: tiempo de espera agotado (timeout)")
		case res := <-protoHandler.ResultChan():
			if res.Error != "" {
				return nil, fmt.Errorf("kiro oauth: error devuelto por proveedor: %s", res.Error)
			}
			code = res.Code
			break waitForAuth
		case manualInput := <-manualChan:
			c, _, e := ParseKiroCallbackURL(manualInput)
			if e != "" {
				return nil, fmt.Errorf("kiro oauth: error en URL pegada: %s", e)
			}
			if c != "" {
				code = c
				break waitForAuth
			}
			fmt.Println("URL no valida. Continua esperando autorizacion...")
		}
	}

	tokenBody, err := json.Marshal(map[string]string{
		"code":          code,
		"code_verifier": verifier,
		"redirect_uri":  KiroRedirectURI,
	})
	if err != nil {
		return nil, fmt.Errorf("kiro oauth: marshal token request: %w", err)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	if cfg != nil {
		httpClient = util.SetProxy(&cfg.SDKConfig, httpClient)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, kiroAuthEndpoint+"/oauth/token", strings.NewReader(string(tokenBody)))
	if err != nil {
		return nil, fmt.Errorf("kiro oauth: new token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "KiroIDE-0.12.333")
	req.Header.Set("Accept", "application/json, text/plain, */*")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiro oauth: token exchange request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kiro oauth: read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kiro oauth: token exchange failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var data struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ProfileArn   string `json:"profileArn"`
		ExpiresIn    int64  `json:"expiresIn"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("kiro oauth: parse token response: %w", err)
	}
	if data.AccessToken == "" {
		return nil, fmt.Errorf("kiro oauth: response did not include an access token")
	}
	expiresIn := data.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()
	region := regionFromProfileArn(data.ProfileArn)
	if region == "" {
		region = defaultRegion
	}
	email := ""
	if profile != nil {
		email = profile.Email
	}
	result := &KiroAuth{
		AccessToken:  data.AccessToken,
		RefreshToken: data.RefreshToken,
		ProfileArn:   data.ProfileArn,
		Region:       region,
		ExpiresAt:    expiresAt,
		AuthMethod:   "social",
		Email:        email,
	}
	WriteKiroCache(result)
	return result, nil
}
