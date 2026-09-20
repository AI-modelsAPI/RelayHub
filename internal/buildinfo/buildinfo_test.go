package buildinfo_test

import (
	"strings"
	"testing"

	"relayhub/internal/buildinfo"
)

// Get must always return a fully populated Info, using safe defaults when the
// build was not stamped with -ldflags.
func TestGetReturnsPopulatedDefaults(t *testing.T) {
	info := buildinfo.Get()

	fields := map[string]string{
		"Name":      info.Name,
		"Version":   info.Version,
		"Commit":    info.Commit,
		"Date":      info.Date,
		"GoVersion": info.GoVersion,
		"OS":        info.OS,
		"Arch":      info.Arch,
	}
	for name, value := range fields {
		if value == "" {
			t.Errorf("build info field %s should not be empty", name)
		}
	}
}

// String must be a single human-readable line that includes the name and version.
func TestStringIncludesNameAndVersion(t *testing.T) {
	info := buildinfo.Get()
	s := info.String()

	if !strings.Contains(s, info.Name) {
		t.Errorf("String() %q does not contain name %q", s, info.Name)
	}
	if !strings.Contains(s, info.Version) {
		t.Errorf("String() %q does not contain version %q", s, info.Version)
	}
	if strings.Contains(s, "\n") {
		t.Errorf("String() %q should be a single line", s)
	}
}
