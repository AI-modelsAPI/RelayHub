package clisync

import (
	"errors"
	"os"
	"strings"
)

// PlaceholderAPIKey marks a preview diff whose real gateway key is only minted
// on apply. Preview must not touch the key service (an abandoned preview would
// leave a live credential behind, AUDIT RH-23/24), so the diff carries this
// marker instead and every syncer refuses to write it to disk.
const PlaceholderAPIKey = "<<issued-on-apply>>"

// ErrNoRealAPIKey is returned by syncers asked to persist an empty or
// placeholder key: a config that "looks synced" but 401s on first use is worse
// than a refused apply.
var ErrNoRealAPIKey = errors.New("refusing to write a CLI credential without a real gateway api key")

// IsPlaceholderKey reports whether key is empty or the preview marker.
func IsPlaceholderKey(key string) bool {
	return strings.TrimSpace(key) == "" || key == PlaceholderAPIKey
}

// UpsertEnvVar sets name=value in a dotenv-style file, replacing an existing
// assignment in place (also the `export NAME=` form) or appending one, and
// writes the result atomically with the engine's 0600 temp-file-and-rename
// path. Missing files are created. Unrelated lines are preserved verbatim.
func (e *Engine) UpsertEnvVar(path, name, value string) error {
	if IsPlaceholderKey(value) {
		return ErrNoRealAPIKey
	}
	content := ""
	if existing, err := os.ReadFile(path); err == nil {
		content = string(existing)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	assignment := name + "=" + value
	lines := strings.Split(content, "\n")
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "export ")
		if strings.HasPrefix(trimmed, name+"=") {
			lines[i] = assignment
			replaced = true
		}
	}
	if !replaced {
		// Keep the file newline-terminated so the new line never glues onto
		// a previous unterminated assignment.
		if content != "" && !strings.HasSuffix(content, "\n") {
			lines = append(lines, "")
		}
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines[len(lines)-1] = assignment
		} else {
			lines = append(lines, assignment)
		}
		lines = append(lines, "")
	}
	return e.WriteAtomic(path, []byte(strings.Join(lines, "\n")))
}

// HasEnvVar reports whether a dotenv-style content assigns name (non-empty).
func HasEnvVar(content, name string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimPrefix(strings.TrimSpace(line), "export ")
		if strings.HasPrefix(trimmed, name+"=") && strings.TrimSpace(strings.TrimPrefix(trimmed, name+"=")) != "" {
			return true
		}
	}
	return false
}
