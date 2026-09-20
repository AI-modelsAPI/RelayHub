// Package buildinfo exposes version and build metadata for the relayhub
// executable. The exported variables are intended to be overridden at build
// time with -ldflags, for example:
//
//	go build -ldflags "-X relayhub/internal/buildinfo.version=1.2.3 \
//	  -X relayhub/internal/buildinfo.commit=$(git rev-parse --short HEAD) \
//	  -X relayhub/internal/buildinfo.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// When the build is not stamped, Get falls back to readable defaults so the
// version information is never empty. This package has no dependencies beyond
// the standard library.
package buildinfo

import (
	"fmt"
	"runtime"
)

// The name reported by the executable.
const appName = "relayhub"

// These variables are overridden at build time via -ldflags. They are lower
// case so they are only writable through the linker or within this package,
// keeping the public surface read-only via Get.
var (
	version = "0.0.0-dev"
	commit  = "unknown"
	date    = "unknown"
)

// Info holds a snapshot of build metadata. All fields are populated by Get.
type Info struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// Get returns a fully populated Info. Fields that were not stamped at build
// time fall back to non-empty defaults so callers can always report a version.
func Get() Info {
	return Info{
		Name:      appName,
		Version:   nonEmpty(version, "0.0.0-dev"),
		Commit:    nonEmpty(commit, "unknown"),
		Date:      nonEmpty(date, "unknown"),
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

// String returns a single-line, human-readable description of the build.
func (i Info) String() string {
	return fmt.Sprintf("%s %s (commit %s, built %s, %s %s/%s)",
		i.Name, i.Version, i.Commit, i.Date, i.GoVersion, i.OS, i.Arch)
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
