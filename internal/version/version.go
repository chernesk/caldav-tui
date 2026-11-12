// Package version reports the build's version from the metadata Go embeds in the
// binary, so there is no constant to keep in sync: tag a release (git tag v0.10.0)
// and build.
package version

import (
	"runtime/debug"
	"strings"
)

// String returns e.g. "0.10.0", "0.10.1-0.20261112…+dirty" for builds past a tag
// or with local changes, or "dev" when no build info is available.
func String() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return fromInfo(info)
}

func fromInfo(info *debug.BuildInfo) string {
	v := strings.TrimPrefix(info.Main.Version, "v")
	if v != "" && v != "(devel)" {
		return v
	}
	// `go run` and some build modes leave the module version unset; fall back to the commit.
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	return "dev-" + rev + dirty
}
