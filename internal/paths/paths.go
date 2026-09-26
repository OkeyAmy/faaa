// Package paths resolves faaa's config, data and cache directories.
package paths

import (
	"os"
	"path/filepath"
)

func base(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return filepath.Join(v, "faaa")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback, "faaa")
}

// Config holds config.json and the hooks dir.
func Config() string { return base("XDG_CONFIG_HOME", ".config") }

// Data holds decoded sounds.
func Data() string { return base("XDG_DATA_HOME", filepath.Join(".local", "share")) }

// Cache holds the catalog index and downloads.
func Cache() string { return base("XDG_CACHE_HOME", ".cache") }

// Hooks is the directory set as the global core.hooksPath.
func Hooks() string { return filepath.Join(Config(), "hooks") }

// Sounds is where decoded, trimmed WAVs live.
func Sounds() string { return filepath.Join(Data(), "sounds") }

// Sound returns the WAV path for a sound id.
func Sound(id string) string { return filepath.Join(Sounds(), id+".wav") }
