package main

import (
	"compress/gzip"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("Failed to load config", "err", err)
		os.Exit(1)
	}

	authClient := NewAuthClient(cfg)
	if err := authClient.auth(); err != nil {
		slog.Error("Auth failed", "err", err)
		os.Exit(1)
	}

	channelData, err := authClient.getChannelData()
	if err != nil {
		slog.Error("Failed to get channel data", "err", err)
		os.Exit(1)
	}

	channels := getChannelList(channelData)

	data := buildM3U(channels, cfg.OutputURL)
	if err := writeM3U(cfg.OutputDir, data); err != nil {
		slog.Error("Failed to write m3u output", "err", err)
		os.Exit(1)
	}

	epgData, err := fetchEPGData(channels, authClient)
	if err != nil {
		slog.Error("Failed to fetch epg data", "err", err)
		os.Exit(1)
	}

	if err := writeEPG(cfg.OutputDir, epgData); err != nil {
		slog.Error("Failed to write epg output", "err", err)
		os.Exit(1)
	}

	notifyPushURL(cfg.PushURL)
}

func writeM3U(outputDir string, data []byte) error {
	outputM3U := filepath.Join(outputDir, "iptv.m3u")

	if err := os.WriteFile(outputM3U, data, 0644); err != nil {
		return err
	}

	slog.Info("Successfully wrote m3u output", "file", outputM3U)
	return nil
}

func writeEPG(outputDir string, data []byte) error {
	outputEPG := filepath.Join(outputDir, "epg.xml.gz")

	file, err := os.Create(outputEPG)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := gzip.NewWriter(file)
	if _, err := writer.Write(data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	slog.Info("Successfully wrote epg output", "file", outputEPG)
	return nil
}

func notifyPushURL(pushURL string) {
	if pushURL == "" {
		return
	}
	resp, err := http.Get(pushURL)
	if err != nil {
		slog.Error("Failed to push URL", "url", pushURL, "err", err)
		return
	}
	resp.Body.Close()
	slog.Info("Successfully pushed URL", "url", pushURL, "status", resp.Status)
}
