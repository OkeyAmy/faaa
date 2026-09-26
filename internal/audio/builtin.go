package audio

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
)

// Builtin sounds ship inside the binary. To change them: drop an mp3/wav
// into internal/audio/builtin/, add {"id","title"} to builtin.json (order
// = display order, first = default), then `go build`.
//
//go:embed builtin/*
var builtinFS embed.FS

type BuiltinSound struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Builtins lists embedded sounds in display order.
func Builtins() []BuiltinSound {
	var out []BuiltinSound
	b, _ := builtinFS.ReadFile("builtin/builtin.json")
	_ = json.Unmarshal(b, &out)
	return out
}

// DefaultID is the first builtin.
func DefaultID() string {
	if b := Builtins(); len(b) > 0 {
		return b[0].ID
	}
	return ""
}

// IsBuiltin reports whether id is embedded.
func IsBuiltin(id string) bool {
	for _, b := range Builtins() {
		if b.ID == id {
			return true
		}
	}
	return false
}

// WriteBuiltin decodes an embedded sound to dst as a normalised WAV.
func WriteBuiltin(id, dst string, maxSec float64) error {
	for _, ext := range []string{".mp3", ".wav"} {
		data, err := builtinFS.ReadFile("builtin/" + id + ext)
		if err != nil {
			continue
		}
		tmp, err := os.CreateTemp("", "faaa-*"+ext)
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(data); err != nil {
			return err
		}
		tmp.Close()
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		_, err = Convert(tmp.Name(), dst, maxSec)
		return err
	}
	return os.ErrNotExist
}
