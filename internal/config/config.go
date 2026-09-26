// Package config loads and saves ~/.config/faaa/config.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/okeyamy/faaa/internal/paths"
)

type Config struct {
	Sound      string   `json:"sound"`                 // selected sound id
	MaxSeconds float64  `json:"max_seconds"`           // clip trim length
	Enabled    bool     `json:"enabled"`               // play on push
	Player     []string `json:"player,omitempty"`      // player command override
	PrevHooks  string   `json:"prev_hooks"`            // user's previous global core.hooksPath
	CatalogURL string   `json:"catalog_url"`           // index.json source
	Theme      string   `json:"theme"`                 // tui palette name
	NoAutoArm  bool     `json:"no_auto_arm,omitempty"` // set by `faaa uninstall`
	// Colors overrides palette slots (fg, dim, accent, accent2, hot, ok, border) with hex.
	Colors map[string]string `json:"colors,omitempty"`
}

const DefaultCatalog = "https://raw.githubusercontent.com/okeyamy/faaa/main/internal/catalog/index.json"

func Default() Config {
	return Config{Sound: "faaah", MaxSeconds: 15, Enabled: true, CatalogURL: DefaultCatalog, Theme: "neon"}
}

func file() string { return filepath.Join(paths.Config(), "config.json") }

// Load returns saved config, or defaults if none exists.
func Load() Config {
	c := Default()
	b, err := os.ReadFile(file())
	if err != nil {
		return c
	}
	_ = json.Unmarshal(b, &c)
	if c.MaxSeconds <= 0 {
		c.MaxSeconds = 15
	}
	if c.CatalogURL == "" {
		c.CatalogURL = DefaultCatalog
	}
	return c
}

func Save(c Config) error {
	if err := os.MkdirAll(paths.Config(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(file(), b, 0o644)
}
