// Resolves MCP server launch details, deduplicated and time-bounded per server.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ResolverManager owns the per-server resolve goroutines. It deduplicates
// concurrent Resolve calls for the same serverID (a second caller gets
// the same in-flight task) and bounds the lifetime of every spawned
// npm subprocess. The resolution work itself is delegated to a Resolver
// picked by ResolverKind; only the npx Resolver is fully implemented,
// the rest are stubbed to return StatusUnsupported.
type ResolverManager struct {
	rootDir string
	timeout time.Duration
	logger  *zap.Logger

	// executors lets tests swap the function that actually spawns npm.
	// nil means use the real exec.CommandContext; non-nil is the test
	// shim path. The shim returns (stdout, stderr, exitErr).
	executor func(ctx context.Context, name string, args ...string) ([]byte, []byte, error)

	// inFlight tracks serverID → cancelFunc. Resolve for a serverID
	// that is already running returns the same channel; the second
	// caller waits for the existing task instead of starting a new one.
	inFlight sync.Map
}

// NewResolverManager constructs a manager that installs resolved npm
// packages into rootDir/<serverID>/<pkg>. rootDir is created lazily on
// the first install; tests may point it at t.TempDir() to avoid
// touching the real userData.
func NewResolverManager(rootDir string) *ResolverManager {
	return &ResolverManager{rootDir: rootDir, timeout: 60 * time.Second}
}

// WithLogger attaches a logger so the manager can report npm failures
// and stale-retry decisions. The fluent style matches the rest of the
// agent so callers can chain NewResolverManager(...).WithLogger(log).
func (r *ResolverManager) WithLogger(l *zap.Logger) *ResolverManager {
	r.logger = l
	return r
}

// withExecutor swaps the spawn function. Tests only.
func (r *ResolverManager) withExecutor(fn func(ctx context.Context, name string, args ...string) ([]byte, []byte, error)) *ResolverManager {
	r.executor = fn
	return r
}

// npxResolver needs a run shim too so tests can swap the spawn without
// touching the manager. In production npxResolver.run is nil and
// Resolve falls back to the package-level runNpx helper below.
func (n *npxResolver) exec(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	if n.run != nil {
		return n.run(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return []byte(stdout.String()), []byte(stderr.String()), err
}

// Resolve starts an asynchronous resolve for spec and returns a channel
// that emits exactly one LaunchResolution (the final state, never a
// streaming progress). The channel is closed after the emit.
//
// If a resolve for the same ServerID is already in flight, Resolve
// returns a channel that mirrors the existing task — second callers do
// not trigger a second install. Stale in-flight entries (e.g. from a
// process that crashed mid-resolve) are picked up by LoadStaleResolutions
// on the next startup.
func (r *ResolverManager) Resolve(ctx context.Context, spec ServerSpec, fingerprint string) <-chan LaunchResolution {
	sub := make(chan LaunchResolution, 1)

	if existing, ok := r.inFlight.Load(spec.ID); ok {
		return attachSubscriber(existing.(*resolveTask), sub)
	}

	task := &resolveTask{subscribers: []chan LaunchResolution{sub}}
	if _, loaded := r.inFlight.LoadOrStore(spec.ID, task); loaded {
		// Lost the race; append to the winner's broadcast list.
		existing, _ := r.inFlight.Load(spec.ID)
		return attachSubscriber(existing.(*resolveTask), sub)
	}

	resolver := r.pickResolver(spec)
	go func() {
		defer r.inFlight.Delete(spec.ID)
		tctx, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()
		task.mu.Lock()
		task.cancel = cancel
		task.mu.Unlock()
		res, _ := resolver.Resolve(tctx, spec)
		if res.ServerID == "" {
			res.ServerID = spec.ID
		}
		if res.ResolverKind == "" {
			res.ResolverKind = resolver.Kind()
		}
		if res.SourceFingerprint == "" {
			res.SourceFingerprint = fingerprint
		}
		if res.UpdatedAt.IsZero() {
			res.UpdatedAt = time.Now()
		}
		if res.Status == "" {
			res.Status = StatusFailed
		}
		task.mu.Lock()
		task.done = true
		task.result = res
		subscribers := task.subscribers
		task.subscribers = nil
		task.mu.Unlock()
		for _, ch := range subscribers {
			ch <- res
			close(ch)
		}
	}()

	return sub
}

// attachSubscriber registers ch on task's broadcast list. If the task has
// already resolved, the stored result is delivered immediately so a late
// subscriber never hangs.
func attachSubscriber(task *resolveTask, ch chan LaunchResolution) <-chan LaunchResolution {
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.done {
		ch <- task.result
		close(ch)
		return ch
	}
	task.subscribers = append(task.subscribers, ch)
	return ch
}

// IsInFlight reports whether a resolve is currently running for
// serverID. LoadStaleResolutions uses this to avoid re-triggering
// resolutions for the same server.
func (r *ResolverManager) IsInFlight(serverID string) bool {
	_, ok := r.inFlight.Load(serverID)
	return ok
}

// Cancel aborts an in-flight resolve for serverID. Returns true if a
// task was found and cancelled; false means the server is either not
// registered or already finished. Used by SetEnabled(false) when a user
// disables a server mid-install.
func (r *ResolverManager) Cancel(serverID string) bool {
	v, ok := r.inFlight.Load(serverID)
	if !ok {
		return false
	}
	task := v.(*resolveTask)
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.cancel != nil {
		task.cancel()
	}
	return true
}

// pickResolver maps the spec to a Resolver. Today only npx is real;
// uvx / go / raw return StatusUnsupported so the registry falls back
// to the spec's original command line.
func (r *ResolverManager) pickResolver(spec ServerSpec) Resolver {
	switch spec.Transport {
	case TransportStdio:
		base := filepath.Base(spec.Command)
		switch base {
		case "npx":
			// Each resolve gets a fresh npxResolver so concurrent
			// servers do not share state. The resolver uses the same
			// executor shim the manager was configured with; nil
			// means production code falls through to the real spawn.
			return &npxResolver{rootDir: r.rootDir, run: r.executor}
		case "uvx", "uv":
			return &stubResolver{kind: ResolverUvx, msg: "uvx resolver not yet implemented"}
		case "go":
			return &stubResolver{kind: ResolverGo, msg: "go resolver not yet implemented"}
		}
		return &stubResolver{kind: ResolverRaw, msg: "raw stdio: no optimisation"}
	default:
		return &stubResolver{kind: ResolverRaw, msg: "non-stdio transport: no optimisation"}
	}
}

type resolveTask struct {
	mu          sync.Mutex
	cancel      context.CancelFunc
	done        bool
	result      LaunchResolution
	subscribers []chan LaunchResolution
}

// Resolver is the per-ResolverKind optimisation step. One concrete
// implementation per ResolverKind; pickResolver picks the right one.
type Resolver interface {
	Kind() ResolverKind
	Resolve(ctx context.Context, spec ServerSpec) (LaunchResolution, error)
}

// stubResolver is the placeholder for ResolverKind values whose
// optimisation is not yet implemented. It never returns an error — the
// status itself is the failure signal so the registry can fall back to
// the raw command without special-casing.
type stubResolver struct {
	kind ResolverKind
	msg  string
}

func (s *stubResolver) Kind() ResolverKind { return s.kind }

func (s *stubResolver) Resolve(_ context.Context, _ ServerSpec) (LaunchResolution, error) {
	return LaunchResolution{
		ResolverKind: s.kind,
		Status:       StatusUnsupported,
		Error:        s.msg,
	}, nil
}

// npxPackage is the parsed form of a single non-flag argument in an
// npx-style command. Version is "latest" if the user did not pin one.
type npxPackage struct {
	Name    string
	Version string
}

// parseNpxArgs extracts the package spec from a list of npx args. The
// first non-flag argument is taken as the package; everything after it
// is preserved as extra args. Scoped packages (@scope/name) and
// version pins (name@1.2.3) are both supported; if no version is
// present, "latest" is returned.
//
//	["-y", "@scope/name@1.0.0", "--flag"] → name=@scope/name, version=1.0.0, extra=[--flag]
//	["-y", "name"]                        → name=name, version=latest, extra=[]
//	["--yes", "@a/b@2.0"]                 → name=@a/b, version=2.0, extra=[]
func parseNpxArgs(args []string) (npxPackage, []string, error) {
	var pkgStr string
	pkgIdx := -1
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		pkgStr = a
		pkgIdx = i
		break
	}
	if pkgStr == "" {
		return npxPackage{}, nil, errors.New("parseNpxArgs: no package spec in args")
	}

	var name, version string
	// Split on the LAST '@' so scoped packages (@scope/name) keep the
	// leading '@'. If the string starts with '@' there is no version
	// unless another '@' follows the slash.
	if strings.HasPrefix(pkgStr, "@") {
		rest := pkgStr[1:]
		idx := strings.LastIndex(rest, "@")
		if idx < 0 {
			name = pkgStr
			version = "latest"
		} else {
			name = "@" + rest[:idx]
			version = rest[idx+1:]
			if version == "" {
				version = "latest"
			}
		}
	} else {
		idx := strings.LastIndex(pkgStr, "@")
		if idx < 0 {
			name = pkgStr
			version = "latest"
		} else {
			name = pkgStr[:idx]
			version = pkgStr[idx+1:]
			if version == "" {
				version = "latest"
			}
		}
	}

	if name == "" {
		return npxPackage{}, nil, errors.New("parseNpxArgs: empty package name")
	}

	extra := append([]string(nil), args[pkgIdx+1:]...)
	return npxPackage{Name: name, Version: version}, extra, nil
}

// npxResolver pre-installs an npx-style MCP server so the runtime
// spawn is `node <abs-bin-path>` instead of paying the npx download
// cost on every launch. It also avoids network round-trips once the
// package is on disk.
//
// The install layout is:
//
//	<rootDir>/<serverID>/node_modules/<name>/package.json
//
// so multiple servers can each have their own scoped copy.
type npxResolver struct {
	rootDir string
	run     func(ctx context.Context, name string, args ...string) ([]byte, []byte, error)
}

func (n *npxResolver) Kind() ResolverKind { return ResolverNpx }

func (n *npxResolver) Resolve(ctx context.Context, spec ServerSpec) (LaunchResolution, error) {
	pkg, extra, err := parseNpxArgs(spec.Args)
	if err != nil {
		return LaunchResolution{
			ResolverKind: ResolverNpx,
			Status:       StatusUnsupported,
			Error:        err.Error(),
		}, nil
	}

	// Step 1: npm view <name>@<version> version --json → resolves "latest" to a real semver.
	viewSpec := pkg.Name + "@" + pkg.Version
	stdout, _, err := n.exec(ctx, "npm", "view", viewSpec, "version", "--json")
	if err != nil {
		return LaunchResolution{
			ResolverKind:     ResolverNpx,
			PackageName:      pkg.Name,
			RequestedVersion: pkg.Version,
			Status:           StatusFailed,
			Error:            fmt.Sprintf("npm view: %v", err),
		}, nil
	}
	resolved := strings.Trim(strings.TrimSpace(string(stdout)), `"`)
	if resolved == "" {
		// `npm view --json` returns a quoted string like "1.2.3"; some
		// npm builds omit the quotes when --json is given a single
		// field. Fall back to the request if the result is empty.
		resolved = pkg.Version
	}

	// Step 2: install into rootDir/<server.id>-<packageName>/ so every
	// (server, package) pair shares a single node_modules across
	// sessions, while a package swap lands in a fresh dir instead of
	// overwriting the prior install. Mirrors LobsterAI's
	// mcpLaunchResolverManager.<server-id>-<packageName> convention.
	installDir := filepath.Join(n.rootDir,
		sanitizeForPath(spec.ID)+"-"+sanitizeForPath(pkg.Name))
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return LaunchResolution{
			ResolverKind:     ResolverNpx,
			PackageName:      pkg.Name,
			RequestedVersion: pkg.Version,
			Status:           StatusFailed,
			Error:            fmt.Sprintf("mkdir: %v", err),
		}, nil
	}
	if _, _, err := n.exec(ctx, "npm", "install",
		"--prefix", installDir,
		"--omit=dev",
		"--no-audit",
		"--no-fund",
		pkg.Name+"@"+resolved,
	); err != nil {
		return LaunchResolution{
			ResolverKind:     ResolverNpx,
			PackageName:      pkg.Name,
			RequestedVersion: pkg.Version,
			ResolvedVersion:  resolved,
			InstallDir:       installDir,
			Status:           StatusFailed,
			Error:            fmt.Sprintf("npm install: %v", err),
		}, nil
	}

	// Step 3: read package.json bin to discover the entry script. The
	// bin field can be a string (one binary) or a map (multiple). The
	// basename of the package (e.g. "name" from "@scope/name") is the
	// conventional key; fall back to the first map entry.
	pkgJSONPath := filepath.Join(installDir, "node_modules", pkg.Name, "package.json")
	raw, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return LaunchResolution{
			ResolverKind:    ResolverNpx,
			PackageName:     pkg.Name,
			ResolvedVersion: resolved,
			InstallDir:      installDir,
			Status:          StatusFailed,
			Error:           "package.json not found after install",
		}, nil
	}
	var pkgJSON struct {
		Bin interface{} `json:"bin"`
	}
	if err := json.Unmarshal(raw, &pkgJSON); err != nil {
		return LaunchResolution{
			ResolverKind:    ResolverNpx,
			PackageName:     pkg.Name,
			ResolvedVersion: resolved,
			InstallDir:      installDir,
			Status:          StatusFailed,
			Error:           "package.json bin: " + err.Error(),
		}, nil
	}
	binRel, err := pickBinEntry(pkgJSON.Bin, pkg.Name)
	if err != nil {
		return LaunchResolution{
			ResolverKind:    ResolverNpx,
			PackageName:     pkg.Name,
			ResolvedVersion: resolved,
			InstallDir:      installDir,
			Status:          StatusFailed,
			Error:           err.Error(),
		}, nil
	}

	binPath := filepath.Join(installDir, "node_modules", pkg.Name, binRel)
	optimisedArgs := append([]string{binPath}, extra...)

	return LaunchResolution{
		ResolverKind:     ResolverNpx,
		PackageName:      pkg.Name,
		RequestedVersion: pkg.Version,
		ResolvedVersion:  resolved,
		InstallDir:       installDir,
		Command:          "node",
		Args:             optimisedArgs,
		ResolvedAt:       time.Now(),
		Status:           StatusReady,
	}, nil
}

// pickBinEntry returns the bin script path declared in a package.json
// `bin` field. bin may be either a string (single entry) or a map of
// name → script. For map form, the entry whose key matches the
// package's basename wins; if no match, the first entry is returned.
func pickBinEntry(bin interface{}, pkgName string) (string, error) {
	switch b := bin.(type) {
	case string:
		if b == "" {
			return "", errors.New("no bin in package.json")
		}
		return b, nil
	case map[string]interface{}:
		// Conventional key: the basename of the package name.
		base := filepath.Base(pkgName) // "@scope/name" → "name"
		if v, ok := b[base].(string); ok && v != "" {
			return v, nil
		}
		for _, v := range b {
			if s, ok := v.(string); ok && s != "" {
				return s, nil
			}
		}
		return "", errors.New("no bin in package.json")
	case nil:
		return "", errors.New("no bin in package.json")
	default:
		return "", fmt.Errorf("unexpected bin type %T", bin)
	}
}
