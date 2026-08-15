package tmux

import "runtime/debug"

// ModulePath is the canonical Go module path for this package.
const ModulePath = "github.com/tmux-python/libtmux/golang"

// PackageVersion returns the release version recorded in Go build metadata.
// Development builds and binaries without module metadata return "", false.
func PackageVersion() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	return packageVersionFromBuildInfo(info)
}

func packageVersionFromBuildInfo(info *debug.BuildInfo) (string, bool) {
	if info == nil {
		return "", false
	}
	if info.Main.Path == ModulePath {
		return releaseModuleVersion(info.Main.Version)
	}
	for _, dependency := range info.Deps {
		if dependency != nil && dependency.Path == ModulePath {
			return releaseModuleVersion(dependency.Version)
		}
	}
	return "", false
}

func releaseModuleVersion(version string) (string, bool) {
	if version == "" || version == "(devel)" {
		return "", false
	}
	return version, true
}
