package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServerManagerFilePasswordFallback(t *testing.T) {
	configDir := t.TempDir()
	manager := NewServerManager("supersonic-test", "test", DefaultConfig("test"), configDir, true, false)
	server := manager.AddServer("Test", ServerConnection{})

	if err := manager.SetServerPassword(server, "secret"); err != nil {
		t.Fatalf("SetServerPassword: %v", err)
	}
	if got, err := manager.GetServerPassword(server.ID); err != nil || got != "secret" {
		t.Fatalf("GetServerPassword = %q, %v; want secret, nil", got, err)
	}
	if info, err := os.Stat(filepath.Join(configDir, "credentials.json")); err != nil {
		t.Fatalf("stat credential file: %v", err)
	} else if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credential file permissions = %o; want 600", got)
	}

	manager.DeleteServer(server.ID)
	if _, err := manager.GetServerPassword(server.ID); err == nil {
		t.Fatal("GetServerPassword succeeded after server deletion")
	}
}

func TestNormalizeServerURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"http://192.168.1.1:8096", "http://192.168.1.1:8096"},
		{"https://music.example.com", "https://music.example.com"},
		{"192.168.1.1:4533", "http://192.168.1.1:4533"},
		{"music.example.com", "http://music.example.com"},
		{"http://192.168.1.1:8096/", "http://192.168.1.1:8096"},
		{"http://192.168.1.1:8096///", "http://192.168.1.1:8096"},
		{"192.168.1.1:8096/", "http://192.168.1.1:8096"},
	}
	for _, tt := range tests {
		got := NormalizeServerURL(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeServerURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeJellyfinURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"http://192.168.1.1:8096", "http://192.168.1.1:8096"},
		{"192.168.1.1:8096", "http://192.168.1.1:8096"},
		{"192.168.1.1:8096/", "http://192.168.1.1:8096"},
		{"192.168.1.1:8096/web/index.html", "http://192.168.1.1:8096"},
		{"http://192.168.1.1:8096/web/index.html", "http://192.168.1.1:8096"},
		{"http://192.168.1.1:8096/web/", "http://192.168.1.1:8096"},
		{"http://192.168.1.1:8096/web", "http://192.168.1.1:8096"},
		{"https://jellyfin.example.com/web/index.html", "https://jellyfin.example.com"},
		{"https://jellyfin.example.com/web/", "https://jellyfin.example.com"},
	}
	for _, tt := range tests {
		got := NormalizeJellyfinURL(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeJellyfinURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
