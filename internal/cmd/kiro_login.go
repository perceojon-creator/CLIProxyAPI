package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	kiroauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/kiro"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	log "github.com/sirupsen/logrus"
)

// DoKiroLogin handles Kiro authentication. If allProfiles is true, it iterates
// through all detected Google Chrome profiles. If interactive, it presents a menu.
func DoKiroLogin(cfg *config.Config, options *LoginOptions, allProfiles bool) {
	if options == nil {
		options = &LoginOptions{}
	}

	profiles, errProfiles := kiroauth.GetChromeProfiles()
	if errProfiles != nil || len(profiles) == 0 {
		doSingleKiroLogin(cfg, options, nil)
		return
	}

	manager := newAuthManager()

	if allProfiles {
		doLoginAllProfiles(cfg, manager, options, profiles)
		return
	}

	fmt.Println("\n=======================================================")
	fmt.Printf("   Kiro AI — Cuentas de Google en Chrome Detectadas (%d)\n", len(profiles))
	fmt.Println("=======================================================")
	fmt.Println(" [0] Iniciar sesión en TODAS las cuentas secuencialmente (Recomendado)")
	for i, p := range profiles {
		fmt.Printf(" [%d] %s (%s) — Perfil: %s\n", i+1, p.Email, p.Name, p.ProfileDir)
	}
	fmt.Printf(" [%d] Importar sesión activa de Kiro Desktop local\n", len(profiles)+1)
	fmt.Println("=======================================================")
	fmt.Print("Elige una opción [0 para todas]: ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" || input == "0" || strings.EqualFold(input, "all") {
		doLoginAllProfiles(cfg, manager, options, profiles)
		return
	}

	choice, errParse := strconv.Atoi(input)
	if errParse != nil || choice < 0 || choice > len(profiles)+1 {
		fmt.Println("Opción inválida. Cancelando.")
		return
	}

	if choice == len(profiles)+1 {
		opts := &LoginOptions{NoBrowser: true}
		doSingleKiroLogin(cfg, opts, nil)
		return
	}

	selected := profiles[choice-1]
	doSingleKiroLogin(cfg, options, &selected)
}

func doSingleKiroLogin(cfg *config.Config, options *LoginOptions, profile *kiroauth.ChromeProfile) {
	manager := newAuthManager()
	authOpts := &sdkAuth.LoginOptions{
		NoBrowser: options.NoBrowser,
		Metadata:  map[string]string{},
		Prompt:    options.Prompt,
	}
	if profile != nil {
		authOpts.Metadata["profile_dir"] = profile.ProfileDir
		authOpts.Metadata["email"] = profile.Email
		authOpts.Metadata["name"] = profile.Name
	}

	record, savedPath, err := manager.Login(context.Background(), "kiro", cfg, authOpts)
	if err != nil {
		log.Errorf("Kiro authentication failed: %v", err)
		return
	}

	if savedPath != "" {
		fmt.Printf("\n✓ Credencial guardada en: %s\n", savedPath)
	}
	if record != nil && record.Label != "" {
		fmt.Printf("✓ Autenticado como: %s\n", record.Label)
	}
	fmt.Println("¡Autenticación de Kiro exitosa!")
}

func doLoginAllProfiles(cfg *config.Config, manager *sdkAuth.Manager, options *LoginOptions, profiles []kiroauth.ChromeProfile) {
	fmt.Printf("\nIniciando proceso de autenticación secuencial para %d cuentas de Google...\n", len(profiles))
	fmt.Println("Para cada cuenta, se abrirá Chrome con ese perfil. Solo autoriza en la ventana.")

	successCount := 0
	reader := bufio.NewReader(os.Stdin)

	for i, p := range profiles {
		fmt.Println("\n----------------------------------------------------------------------")
		fmt.Printf("[%d/%d] Iniciando para: %s (%s) [Perfil: %s]\n", i+1, len(profiles), p.Email, p.Name, p.ProfileDir)
		fmt.Println("----------------------------------------------------------------------")

		authOpts := &sdkAuth.LoginOptions{
			NoBrowser: options.NoBrowser,
			Metadata: map[string]string{
				"profile_dir": p.ProfileDir,
				"email":       p.Email,
				"name":        p.Name,
			},
			Prompt: options.Prompt,
		}

		record, savedPath, err := manager.Login(context.Background(), "kiro", cfg, authOpts)
		if err != nil {
			log.Errorf("Fallo al autenticar %s: %v", p.Email, err)
			if i < len(profiles)-1 {
				fmt.Print("¿Deseas continuar con la siguiente cuenta? (Enter = Sí, n = Salir): ")
				ans, _ := reader.ReadString('\n')
				ans = strings.TrimSpace(strings.ToLower(ans))
				if ans == "n" || ans == "no" {
					break
				}
			}
			continue
		}

		successCount++
		if savedPath != "" {
			fmt.Printf("✓ Credencial guardada: %s\n", savedPath)
		}
		if record != nil && record.Label != "" {
			fmt.Printf("✓ Vinculada: %s\n", record.Label)
		}
	}

	fmt.Println("\n=======================================================")
	fmt.Printf("Resumen: %d de %d cuentas vinculadas exitosamente.\n", successCount, len(profiles))
	fmt.Printf("Total estimado de créditos en pool: ~%d créditos.\n", successCount*50)
	fmt.Println("=======================================================")
}
