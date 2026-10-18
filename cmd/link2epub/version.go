package main

import "runtime/debug"

// version is set at build time by release builds and build.cmd:
//
//	go build -ldflags "-X main.version=v1.0.0" ./cmd/link2epub
var version string

// versionString reports the version: the one set at build time, else the
// module version for "go install ...@v1.0.0", else the git commit the
// binary was built from.
func versionString() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = "-dirty"
			}
		}
	}
	if rev == "" {
		return "dev"
	}
	return "dev-" + rev[:min(len(rev), 12)] + modified
}
