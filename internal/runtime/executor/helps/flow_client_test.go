package helps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/flow"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestFlowClient_ParseAssetInner(t *testing.T) {
	client := &FlowClient{}
	info := &FlowAssetInfo{}

	mockInner := `["b2d3e69e-04c8-4a77-ae93-c235c04f8444","9dd588d0-405b-4fb9-95f7-063bd0ab8f33",null,null,null,null,null,[[null,null,null,null,null,null,null,"<root><instruction><prompt>cyberpunk falcon</prompt></instruction></root>","https://flow-content.google/video/b2d3e69e?Expires=123&KeyName=labs&Signature=abc",null,null,null,"veo_3_1_t2v_lite","",null,false,2],null,["https://flow-content.google/image/b2d3e69e?Expires=123&KeyName=labs&Signature=def"]]]`

	client.parseAssetInner(mockInner, info)

	if info.ProjectID != "9dd588d0-405b-4fb9-95f7-063bd0ab8f33" {
		t.Fatalf("unexpected project ID: %s", info.ProjectID)
	}
	if !strings.Contains(info.VideoURL, "/video/b2d3e69e") {
		t.Fatalf("unexpected video url: %s", info.VideoURL)
	}
	if !strings.Contains(info.ImageURL, "/image/b2d3e69e") {
		t.Fatalf("unexpected image url: %s", info.ImageURL)
	}
	if info.Prompt != "cyberpunk falcon" {
		t.Fatalf("unexpected prompt: %s", info.Prompt)
	}
	if info.Model != "veo_3_1_t2v_lite" {
		t.Fatalf("unexpected model: %s", info.Model)
	}
}

func TestFlowClient_DownloadAssetMock(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-jpeg-bytes"))
	}))
	defer ts.Close()

	auth := &cliproxyauth.Auth{
		Storage: &flow.FlowTokenStorage{
			Cookies: "test-cookie",
		},
	}
	client, err := NewFlowClient(&config.Config{}, auth)
	if err != nil {
		t.Fatalf("NewFlowClient failed: %v", err)
	}

	data, cType, errDl := client.DownloadAsset(context.Background(), ts.URL)
	if errDl != nil {
		t.Fatalf("DownloadAsset failed: %v", errDl)
	}
	if string(data) != "fake-jpeg-bytes" {
		t.Fatalf("unexpected data: %s", string(data))
	}
	if cType != "image/jpeg" {
		t.Fatalf("unexpected content type: %s", cType)
	}
}
