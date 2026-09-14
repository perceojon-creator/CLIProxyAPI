package flow

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	log "github.com/sirupsen/logrus"
)

const defaultSyncPort = 51122

var (
	reCookieHdr   = regexp.MustCompile(`(?i)-H\s+['"][Cc]ookie:\s*([^'"]+)['"]`)
	reCookieFlag  = regexp.MustCompile(`(?i)(?:-b|--cookie)\s+['"]([^'"]+)['"]`)
	reAtTokenForm = regexp.MustCompile(`at=([^&'"\s]+)`)
)

// ExtractCookiesFromInput parses cookies and at_token from a raw cURL command, Cookie header, or session token.
func ExtractCookiesFromInput(input string) (cookies string, atToken string) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(strings.ToLower(input), "curl") || strings.Contains(input, " -H ") || strings.Contains(input, " -b ") {
		if m := reCookieHdr.FindStringSubmatch(input); len(m) > 1 {
			cookies = m[1]
		} else if m := reCookieFlag.FindStringSubmatch(input); len(m) > 1 {
			cookies = m[1]
		}
		if m := reAtTokenForm.FindStringSubmatch(input); len(m) > 1 {
			if dec, err := url.QueryUnescape(m[1]); err == nil {
				atToken = dec
			} else {
				atToken = m[1]
			}
		}
	} else if strings.HasPrefix(strings.ToLower(input), "cookie:") {
		cookies = strings.TrimSpace(input[7:])
	} else if strings.Contains(input, "SID=") || strings.Contains(input, "SAPISID=") || strings.Contains(input, "__Secure-") {
		cookies = input
	} else {
		cookies = input
	}
	return strings.TrimSpace(cookies), strings.TrimSpace(atToken)
}

// HarvestProfile guides authentication for a single Chrome profile and captures its session.
func HarvestProfile(ctx context.Context, cfg *config.Config, profile *ChromeProfile, noBrowser bool) (*FlowAuth, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	syncServer := NewSyncServer(cfg, defaultSyncPort)
	_ = syncServer.Start()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = syncServer.Stop(shutCtx)
	}()

	fmt.Println("=======================================================")
	if profile != nil {
		fmt.Printf("   Google Flow — Autenticando perfil: %s (%s)\n", profile.Email, profile.ProfileDir)
	} else {
		fmt.Println("   Google Flow — Autenticacion de cuenta Google")
	}
	fmt.Printf("   Bridge de sincronizacion local activo en: http://127.0.0.1:%d/auth/flow/sync\n", defaultSyncPort)
	fmt.Println("=======================================================")

	if !noBrowser {
		if profile != nil && profile.ProfileDir != "" {
			fmt.Printf("Abriendo Chrome con perfil '%s' en Google Flow...\n", profile.ProfileDir)
			if errOpen := OpenFlowInChromeProfile(profile.ProfileDir); errOpen != nil {
				log.Warnf("No se pudo abrir Chrome con perfil %s: %v. Abriendo navegador predeterminado...", profile.ProfileDir, errOpen)
				_ = browser.OpenURL(FlowAppURL)
			}
		} else {
			fmt.Println("Abriendo navegador en Google Flow...")
			_ = browser.OpenURL(FlowAppURL)
		}
	} else {
		fmt.Printf("Abre la siguiente URL en el perfil correspondiente:\n%s\n", FlowAppURL)
	}

	fmt.Println("\nPuedes:")
	fmt.Println(" 1. Iniciar sesion en la pestana abierta de Google Flow.")
	fmt.Println(" 2. Pegar directamente aqui:")
	fmt.Println("    - El comando cURL copiado desde DevTools ('Copy as cURL')")
	fmt.Println("    - O el encabezado Cookie completo (Cookie: SID=...)")
	fmt.Println("    - O la cookie de sesion")
	fmt.Print("Pega aqui tu cURL o Cookies (o presiona Enter si usas bridge/sync): ")

	inputChan := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		inputChan <- strings.TrimSpace(line)
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case auth := <-syncServer.SyncedChan():
		if auth != nil {
			fmt.Printf("\nOK: Session received automatically via bridge for: %s (%s)\n", auth.Email, auth.Name)
			return auth, nil
		}
		return nil, fmt.Errorf("invalid session received from bridge")
	case rawInput := <-inputChan:
		if rawInput != "" {
			cookies, atToken := ExtractCookiesFromInput(rawInput)
			profileDir := ""
			email := ""
			name := ""
			if profile != nil {
				profileDir = profile.ProfileDir
				email = profile.Email
				name = profile.Name
			}
			res, err := HandleSyncPayload(ctx, cfg, SyncRequest{
				Cookies:      cookies,
				SessionToken: cookies,
				AtToken:      atToken,
				Email:        email,
				Name:         name,
				ProfileDir:   profileDir,
			})
			if err != nil {
				return nil, fmt.Errorf("error validando sesion de Flow: %w", err)
			}
			fmt.Printf("OK: Credenciales guardadas exitosamente en: %s\n", res.Path)
			return ResolveFlowAuth(res.Path)
		}
		return nil, fmt.Errorf("no se recibio token de sesion")
	case <-time.After(3 * time.Minute):
		return nil, fmt.Errorf("tiempo de espera agotado esperando sesion de Flow")
	}
}
