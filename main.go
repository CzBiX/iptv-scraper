package main

import (
	"log/slog"
	"net/http"
	"os"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("Failed to load config", "err", err)
		os.Exit(1)
	}

	authClient := NewAuthClient(cfg)
	baseURL, err := authClient.auth()
	if err != nil {
		slog.Error("Auth failed", "err", err)
		os.Exit(1)
	}

	channelData, err := authClient.getChannelData(baseURL)
	if err != nil {
		slog.Error("Failed to get channel data", "err", err)
		os.Exit(1)
	}

	m3u := getChannelList(channelData)

	if err := os.WriteFile(cfg.Output, []byte(m3u), 0644); err != nil {
		slog.Error("Failed to write output", "err", err)
		os.Exit(1)
	}
	slog.Info("Successfully wrote output", "file", cfg.Output)

	if cfg.PushURL != "" {
		resp, err := http.Get(cfg.PushURL)
		if err != nil {
			slog.Error("Failed to push URL", "url", cfg.PushURL, "err", err)
		} else {
			resp.Body.Close()
			slog.Info("Successfully pushed URL", "url", cfg.PushURL, "status", resp.Status)
		}
	}
}
