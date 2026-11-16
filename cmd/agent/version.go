package main

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
)

// Filled by package-release.sh through -ldflags "-X main.version=vX.Y.Z ...".
var (
	version   = "dev"
	commit    = ""
	buildDate = ""
)

type buildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func currentBuild() buildInfo {
	info := buildInfo{Version: version, Commit: commit, Date: buildDate, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if info.Commit != "" {
		return info
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		dirty := false
		for _, setting := range bi.Settings {
			switch setting.Key {
			case "vcs.revision":
				info.Commit = setting.Value
			case "vcs.modified":
				dirty = setting.Value == "true"
			case "vcs.time":
				if info.Date == "" {
					info.Date = setting.Value
				}
			}
		}
		if dirty && info.Commit != "" {
			info.Commit += "+dirty"
		}
	}
	return info
}

func shortCommit(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

// versionLine is the first line of --version; install.sh parses it.
func versionLine() string {
	return "herdr-recall " + version
}

func printVersion(out io.Writer, long bool) {
	fmt.Fprintln(out, versionLine())
	if !long {
		return
	}
	info := currentBuild()
	if info.Commit != "" {
		fmt.Fprintf(out, "commit  %s\n", shortCommit(info.Commit))
	}
	if info.Date != "" {
		fmt.Fprintf(out, "build   %s\n", info.Date)
	}
	fmt.Fprintf(out, "go      %s %s/%s\n", info.Go, info.OS, info.Arch)
}
