package flow

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	log "github.com/sirupsen/logrus"
)

const defaultSyncPort = 51122

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
	fmt.Println(" 2. Pegar directamente aqui el valor de la cookie '__Secure-next-auth.session-token':")
	fmt.Print("Token de sesion (o presiona Enter si usas la extension/sync): ")

	inputChan := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		inputChan <- strings.TrimSpace(line)
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case token := <-inputChan:
		if token != "" {
			// Strip cookie name prefix if user pasted full Cookie header
			if strings.Contains(token, "=") {
				parts := strings.Split(token, ";")
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if strings.HasPrefix(p, SessionCookieName+"=") {
						token = strings.TrimPrefix(p, SessionCookieName+"=")
						break
					}
				}
			}
			fmt.Println("Verificando token de sesion contra labs.google...")
			profileDir := ""
			email := ""
			if profile != nil {
				profileDir = profile.ProfileDir
				email = profile.Email
			}
			res, err := HandleSyncPayload(ctx, cfg, SyncRequest{
				SessionToken: token,
				Email:        email,
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
