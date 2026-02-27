package main

import (
	"encoding/json"
	"fmt"
	"os"
)

const configFile = "config.json"

type Config struct {
	UserID string `json:"user_id"`
	Key    string `json:"key"`

	StbID string `json:"stb_id"`
	Mac   string `json:"mac"`

	LoginURL  string `json:"login_url"`
	UserAgent string `json:"user_agent"`

	RouteIP   string `json:"route_ip"`
	IPTVIPURL string `json:"iptv_ip_url"`
	Output    string `json:"output"`

	// optional, for healthcheck
	PushURL string `json:"push_url"`
}

func loadConfig() (*Config, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	// Basic validation
	if cfg.UserID == "" || cfg.Key == "" || cfg.StbID == "" || cfg.Mac == "" || cfg.LoginURL == "" || cfg.UserAgent == "" || cfg.RouteIP == "" || cfg.Output == "" {
		return nil, fmt.Errorf("missing required fields in config: %+v", cfg)
	}
	return &cfg, nil
}
