package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/flow"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	log "github.com/sirupsen/logrus"
)

// DoFlowLogin handles Google Flow authentication.
// If allProfiles is true, it iterates through all detected Google Chrome profiles.
func DoFlowLogin(cfg *config.Config, options *LoginOptions, allProfiles bool) {
	if options == nil {
		options = &LoginOptions{}
	}

	profiles, errProfiles := flow.GetChromeProfiles()
	if errProfiles != nil || len(profiles) == 0 {
		doSingleFlowLogin(cfg, options, nil)
		return
	}

	manager := newAuthManager()

	if allProfiles {
		doLoginAllFlowProfiles(cfg, manager, options, profiles)
		return
	}

	fmt.Println("\n=======================================================")
	fmt.Printf("   Google Flow — Cuentas de Google en Chrome Detectadas (%d)\n", len(profiles))
	fmt.Println("=======================================================")
	fmt.Println(" [0] Iniciar sesion / Cosechar TODAS las cuentas secuencialmente (Recomendado)")
	for i, p := range profiles {
		fmt.Printf(" [%d] %s (%s) — Perfil: %s\n", i+1, p.Email, p.Name, p.ProfileDir)
	}
	fmt.Printf(" [%d] Iniciar servidor local de sincronizacion (bridge)\n", len(profiles)+1)
	fmt.Println("=======================================================")
	fmt.Print("Elige una opcion [0 para todas]: ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" || input == "0" || strings.EqualFold(input, "all") {
		doLoginAllFlowProfiles(cfg, manager, options, profiles)
		return
	}

	choice, errParse := strconv.Atoi(input)
	if errParse != nil || choice < 0 || choice > len(profiles)+1 {
		fmt.Println("Opcion invalida. Cancelando.")
		return
	}

	if choice == len(profiles)+1 {
		server := flow.NewSyncServer(cfg, 51122)
		fmt.Println("Iniciando servidor de sincronizacion en http://127.0.0.1:51122...")
		fmt.Println("Presiona Enter para detener.")
		_ = server.Start()
		_, _ = reader.ReadString('\n')
		_ = server.Stop(context.Background())
		return
	}

	selected := profiles[choice-1]
	doSingleFlowLogin(cfg, options, &selected)
}

func doSingleFlowLogin(cfg *config.Config, options *LoginOptions, profile *flow.ChromeProfile) {
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

	record, savedPath, err := manager.Login(context.Background(), "flow", cfg, authOpts)
	if err != nil {
		log.Errorf("Google Flow authentication failed: %v", err)
		return
	}
	if savedPath != "" {
		fmt.Printf("Credenciales de Google Flow guardadas en: %s\n", savedPath)
	} else if record != nil {
		fmt.Printf("Credenciales de Google Flow listas para: %s\n", record.ID)
	}
}

func doLoginAllFlowProfiles(cfg *config.Config, manager *sdkAuth.Manager, options *LoginOptions, profiles []flow.ChromeProfile) {
	fmt.Printf("\nIniciando proceso de autenticacion para %d perfiles de Google Flow...\n", len(profiles))
	successCount := 0
	for i, profile := range profiles {
		fmt.Println("-------------------------------------------------------")
		fmt.Printf("[%d/%d] Perfil: %s (%s) — %s\n", i+1, len(profiles), profile.Email, profile.Name, profile.ProfileDir)
		fmt.Println("-------------------------------------------------------")

		authOpts := &sdkAuth.LoginOptions{
			NoBrowser: options.NoBrowser,
			Metadata: map[string]string{
				"profile_dir": profile.ProfileDir,
				"email":       profile.Email,
				"name":        profile.Name,
			},
			Prompt: options.Prompt,
		}

		record, savedPath, err := manager.Login(context.Background(), "flow", cfg, authOpts)
		if err != nil {
			log.Errorf("Error autenticando perfil %s: %v", profile.Email, err)
			fmt.Printf("Deseas reintentar este perfil? (s/N): ")
			reader := bufio.NewReader(os.Stdin)
			retry, _ := reader.ReadString('\n')
			if strings.EqualFold(strings.TrimSpace(retry), "s") {
				record, savedPath, err = manager.Login(context.Background(), "flow", cfg, authOpts)
			}
		}
		if err == nil && (record != nil || savedPath != "") {
			successCount++
			fmt.Printf("OK: Perfil %s configurado correctamente.\n", profile.Email)
		}
	}
	fmt.Println("\n=======================================================")
	fmt.Printf("   Resumen: %d/%d cuentas de Google Flow autenticadas exitosamente.\n", successCount, len(profiles))
	fmt.Println("=======================================================")
}
