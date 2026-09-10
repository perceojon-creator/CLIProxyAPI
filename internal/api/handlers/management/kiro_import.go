package management

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	kiroauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/kiro"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// ImportKiroCredential reads the local Kiro desktop session and saves it as an auth record.
// Kiro has no browser OAuth flow: the desktop app (or an AWS SSO login) already wrote a
// bearer token to disk, so this endpoint imports it instead of returning an auth URL.
func (h *Handler) ImportKiroCredential(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "config unavailable"})
		return
	}
	if h.cfg.AuthDir == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "auth directory not configured"})
		return
	}

	ctx := context.Background()
	if reqCtx := c.Request.Context(); reqCtx != nil {
		ctx = reqCtx
	}
	ctx = PopulateAuthContext(ctx, c)

	resolved, err := kiroauth.ResolveKiroAuth(nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kiro_session_unavailable", "message": err.Error()})
		return
	}

	storage := kiroauth.NewTokenStorage(resolved)
	metadata := map[string]any{
		"type":          "kiro",
		"access_token":  resolved.AccessToken,
		"refresh_token": resolved.RefreshToken,
		"region":        resolved.Region,
		"auth_method":   resolved.AuthMethod,
		"timestamp":     time.Now().UnixMilli(),
	}
	if profileArn := strings.TrimSpace(resolved.ProfileArn); profileArn != "" {
		metadata["profile_arn"] = profileArn
	}
	if storage != nil && storage.Expired != "" {
		metadata["expired"] = storage.Expired
	}

	label := fmt.Sprintf("Kiro User (%s/%s)", resolved.AuthMethod, resolved.Region)
	fileName := fmt.Sprintf("kiro-%d.json", time.Now().UnixMilli())
	record := &coreauth.Auth{
		ID:       fileName,
		Provider: "kiro",
		FileName: fileName,
		Label:    label,
		Storage:  storage,
		Metadata: metadata,
	}

	savedPath, errSave := h.saveTokenRecord(ctx, record)
	if errSave != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed", "message": errSave.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":      "ok",
		"auth-file":   savedPath,
		"label":       label,
		"region":      resolved.Region,
		"auth_method": resolved.AuthMethod,
	})
}
