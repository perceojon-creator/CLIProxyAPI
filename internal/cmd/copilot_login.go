package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	kiroauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/kiro"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	log "github.com/sirupsen/logrus"
)

func DoCopilotLogin(cfg *config.Config, options *LoginOptions, allProfiles bool) {
	if options == nil {
		options = &LoginOptions{}
	}

	profiles, errProfiles := kiroauth.GetChromeProfiles()
	if errProfiles != nil || len(profiles) == 0 {
		doSingleCopilotLogin(cfg, options, nil)
		return
	}

	manager := newAuthManager()

	if allProfiles {
		doLoginAllCopilotProfiles(cfg, manager, options, profiles)
		return
	}

	fmt.Println()
	fmt.Println("=======================================================")
	fmt.Printf("   GitHub Copilot - Perfiles de Navegador Detectados (%d)\n", len(profiles))
	fmt.Println("=======================================================")
	fmt.Println(" [0] Iniciar sesion en TODOS los perfiles secuencialmente (Recomendado)")
	for i, p := range profiles {
		fmt.Printf(" [%d] %s (%s) - Perfil: %s\n", i+1, p.Email, p.Name, p.ProfileDir)
	}
	fmt.Printf(" [%d] Iniciar sesion con navegador predeterminado / deteccion local\n", len(profiles)+1)
	fmt.Println("=======================================================")
	fmt.Print("Elige una opcion [0 para todos]: ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString(byte('\n'))
	input = strings.TrimSpace(input)

	if input == "" || input == "0" || strings.EqualFold(input, "all") {
		doLoginAllCopilotProfiles(cfg, manager, options, profiles)
		return
	}

	choice, errParse := strconv.Atoi(input)
	if errParse != nil || choice < 0 || choice > len(profiles)+1 {
		fmt.Println("Opcion invalida. Cancelando.")
		return
	}

	if choice == len(profiles)+1 {
		doSingleCopilotLogin(cfg, options, nil)
		return
	}

	selected := profiles[choice-1]
	doSingleCopilotLogin(cfg, options, &selected)
}

func doSingleCopilotLogin(cfg *config.Config, options *LoginOptions, profile *kiroauth.ChromeProfile) {
	manager := newAuthManager()
	authOpts := &sdkAuth.LoginOptions{
		NoBrowser:    options.NoBrowser,
		CallbackPort: options.CallbackPort,
		Metadata:     map[string]string{},
		Prompt:       options.Prompt,
		ForceBrowser: options.ForceBrowser,
	}
	if profile != nil {
		authOpts.Metadata["profile_dir"] = profile.ProfileDir
		authOpts.Metadata["email"] = profile.Email
		authOpts.Metadata["name"] = profile.Name
		authOpts.ForceBrowser = true
	}

	record, savedPath, err := manager.Login(context.Background(), "copilot", cfg, authOpts)
	if err != nil {
		log.Errorf("GitHub Copilot authentication failed: %v", err)
		return
	}

	if savedPath != "" {
		fmt.Printf("\nCredencial guardada en: %s\n", savedPath)
	}
	if record != nil && record.Label != "" {
		fmt.Printf("Autenticado como: %s\n", record.Label)
	}
	fmt.Println("Autenticacion de GitHub Copilot exitosa!")
}

func doLoginAllCopilotProfiles(cfg *config.Config, manager *sdkAuth.Manager, options *LoginOptions, profiles []kiroauth.ChromeProfile) {
	fmt.Printf("\nIniciando proceso de autenticacion secuencial para %d perfiles de navegador...\n", len(profiles))
	fmt.Println("Para cada cuenta, se abrira el navegador con ese perfil para autorizar en GitHub.")

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
			Prompt:       options.Prompt,
			ForceBrowser: true,
		}

		record, savedPath, err := manager.Login(context.Background(), "copilot", cfg, authOpts)
		if err != nil {
			log.Errorf("[%d/%d] Error autenticando perfil %s: %v", i+1, len(profiles), p.Email, err)
			fmt.Print("Deseas continuar con el siguiente perfil? [S/n]: ")
			contInput, _ := reader.ReadString(byte('\n'))
			contInput = strings.TrimSpace(strings.ToLower(contInput))
			if contInput == "n" || contInput == "no" {
				fmt.Println("Proceso secuencial interrumpido por el usuario.")
				break
			}
			continue
		}

		successCount++
		if savedPath != "" {
			fmt.Printf("[%d/%d] Guardado en: %s\n", i+1, len(profiles), savedPath)
		}
		if record != nil && record.Label != "" {
			fmt.Printf("[%d/%d] Autenticado como: %s\n", i+1, len(profiles), record.Label)
		}

		if i < len(profiles)-1 {
			fmt.Println("\nAvanzando al siguiente perfil en 3 segundos...")
			time.Sleep(3 * time.Second)
		}
	}

	fmt.Println("\n=======================================================")
	fmt.Printf("   Resumen: %d de %d perfiles autenticados con exito\n", successCount, len(profiles))
	fmt.Println("=======================================================")
}
