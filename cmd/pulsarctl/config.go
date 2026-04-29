package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type credentials struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "pulsar", "config.json")
}

func loadCreds() (*credentials, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil, fmt.Errorf("not logged in — run: pulsarctl login")
	}
	var c credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("corrupt config: %w", err)
	}
	return &c, nil
}

func saveCreds(c *credentials) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func deleteCreds() error {
	return os.Remove(configPath())
}
