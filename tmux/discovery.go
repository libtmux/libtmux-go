package tmux

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// DiscoveryOptions bounds a nonrecursive scan of socket directories.
// Nil Roots scans configured roots; nonnil Roots scans the supplied directories.
// IncludeConfigured adds the captured named-socket directory (TMUX_TMPDIR or
// /tmp, followed by tmux-UID) and the selected endpoint's parent.
// It does not scan other users or the machine.
type DiscoveryOptions struct {
	// Roots contains absolute directories. Nil selects the captured configured roots.
	Roots []string
	// IncludeConfigured adds captured configured roots to an explicit Roots slice.
	IncludeConfigured bool
	// FollowSocketSymlinks permits candidate symlinks whose target is a socket.
	FollowSocketSymlinks bool
	// MaxEntries defaults to 256 and bounds directory entries, including skips.
	MaxEntries int
	// MaxProbes defaults to 64 and bounds candidate probes.
	MaxProbes int
	// Timeout defaults to five seconds for the complete scan.
	Timeout time.Duration
	// ProbeTimeout defaults to 250 milliseconds within the overall deadline.
	ProbeTimeout time.Duration
}

// DiscoveryDiagnostic describes a root or candidate that did not yield a server.
// Kind is root, skipped, symlink, duplicate, probe, or limit. Err retains probe
// and filesystem failures, including stale sockets; an empty root has no error.
type DiscoveryDiagnostic struct {
	// Path identifies the candidate or root.
	Path string
	// Kind classifies the diagnostic as documented on DiscoveryDiagnostic.
	Kind string
	// DuplicateOf names the earlier path for a duplicate diagnostic.
	DuplicateOf string
	// Err retains the underlying failure, or nil for a deliberate skip or bound.
	Err error
}

// DiscoveryResult contains borrowed daemon-bound handles and scan diagnostics.
// Truncated reports an entry, probe, or time bound; Entries and Probes count work.
// Directory order is filesystem order. Servers contains each daemon only once.
type DiscoveryResult struct {
	// Servers contains one borrowed handle per responding daemon.
	Servers []Server
	// Diagnostics retains root failures, skips, probe failures and duplicate paths.
	Diagnostics []DiscoveryDiagnostic
	// Truncated reports that an entry, probe or time bound stopped the scan.
	Truncated bool
	// Entries counts directory entries considered, including skips.
	Entries int
	// Probes counts socket candidates queried.
	Probes int
}

// Discover searches socket directories with read-only, no-start identity probes.
// A candidate cannot start tmux. Explicit root symlinks follow normal filesystem
// semantics; entry symlinks are skipped unless FollowSocketSymlinks is true.
// Duplicate paths and aliases of one daemon produce diagnostics. Root and probe
// failures remain in the result; invalid options return an error before scanning.
// Cancellation or a scan deadline returns the partial result and its context error.
func (s Server) Discover(ctx context.Context, options DiscoveryOptions) (DiscoveryResult, error) {
	var result DiscoveryResult
	state, err := s.stateForUse()
	if err != nil {
		return result, err
	}
	if options.MaxEntries < 0 || options.MaxProbes < 0 || options.Timeout < 0 || options.ProbeTimeout < 0 {
		return result, invalidLifecycleRequest("discovery bounds must be nonnegative")
	}
	if options.MaxEntries == 0 {
		options.MaxEntries = 256
	}
	if options.MaxProbes == 0 {
		options.MaxProbes = 64
	}
	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}
	if options.ProbeTimeout == 0 {
		options.ProbeTimeout = 250 * time.Millisecond
	}
	roots := append([]string(nil), options.Roots...)
	if options.Roots == nil || options.IncludeConfigured {
		parent, _ := filepath.Split(s.SocketPath())
		if len(parent) > 1 {
			parent = parent[:len(parent)-1]
		}
		roots = append(roots, state.config.socketSelection.NamedDirectory, parent)
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			return result, invalidLifecycleRequest("discovery roots must be absolute")
		}
	}
	scan, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	seenRoots := map[string]bool{}
	seenDaemons := map[string]string{}
	for _, root := range roots {
		if err := scan.Err(); err != nil {
			result.Truncated = true
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: root, Kind: "limit", Err: err})
			return result, err
		}
		if seenRoots[root] {
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: root, Kind: "duplicate", DuplicateOf: root})
			continue
		}
		seenRoots[root] = true
		info, err := os.Stat(root)
		if err == nil && !info.IsDir() {
			err = errors.New("tmux: discovery root is not a directory")
		}
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: root, Kind: "root", Err: err})
			continue
		}
		directory, err := os.Open(root)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: root, Kind: "root", Err: err})
			continue
		}
		err = s.discoverDirectory(scan, directory, root, options, &result, seenDaemons)
		closeErr := directory.Close()
		if closeErr != nil {
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: root, Kind: "root", Err: closeErr})
		}
		if err != nil {
			return result, err
		}
		if result.Truncated {
			return result, nil
		}
	}
	return result, nil
}

func (s Server) discoverDirectory(ctx context.Context, directory *os.File, root string, options DiscoveryOptions, result *DiscoveryResult, seen map[string]string) error {
	for {
		if err := ctx.Err(); err != nil {
			result.Truncated = true
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: root, Kind: "limit", Err: err})
			return err
		}
		// Read one extra entry to distinguish a full directory from an exhausted bound.
		entries, err := directory.ReadDir(1)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: root, Kind: "root", Err: err})
			//nolint:nilerr // Per-root errors remain in Diagnostics while the scan continues.
			return nil
		}
		path := appendSocketComponent(root, entries[0].Name())
		if result.Entries >= options.MaxEntries {
			result.Truncated = true
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: path, Kind: "limit"})
			return nil
		}
		result.Entries++
		entry := entries[0]
		mode := entry.Type()
		if mode == 0 {
			info, err := entry.Info()
			if err != nil {
				result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: path, Kind: "probe", Err: err})
				continue
			}
			mode = info.Mode()
		}
		if mode&os.ModeSymlink != 0 {
			if !options.FollowSocketSymlinks {
				result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: path, Kind: "symlink"})
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: path, Kind: "probe", Err: err})
				continue
			}
			mode = info.Mode()
		}
		if mode&os.ModeSocket == 0 {
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: path, Kind: "skipped"})
			continue
		}
		if result.Probes >= options.MaxProbes {
			result.Truncated = true
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: path, Kind: "limit"})
			return nil
		}
		result.Probes++
		candidate, err := s.WithSocketPath(path)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: path, Kind: "probe", Err: err})
			continue
		}
		probe, cancel := context.WithTimeout(ctx, options.ProbeTimeout)
		identity, err := candidate.probeSnapshotIdentity(probe)
		cancel()
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: path, Kind: "probe", Err: err})
			continue
		}
		key := formatSnapshotIdentity(identity)
		if prior, ok := seen[key]; ok {
			result.Diagnostics = append(result.Diagnostics, DiscoveryDiagnostic{Path: path, Kind: "duplicate", DuplicateOf: prior})
			continue
		}
		seen[key] = path
		result.Servers = append(result.Servers, candidate.withDaemon(identity))
	}
}
