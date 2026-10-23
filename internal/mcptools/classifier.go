package mcptools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Effect classifies what a tool call can do to the world. External MCP tools
// are always "external": their process behavior cannot be constrained from
// inside IceTudio.
type Effect string

const (
	EffectPure     Effect = "pure"
	EffectRead     Effect = "read"
	EffectWrite    Effect = "write"
	EffectExecute  Effect = "execute"
	EffectExternal Effect = "external"
	EffectUnknown  Effect = "unknown"
)

// Sensitivity classifies the data a tool result carries, decided from the
// actual call target — never from the tool's own description. Unknown reads
// never auto-execute and credentials never leave the machine without an
// explicit user decision.
type Sensitivity string

const (
	SensPublic     Sensitivity = "public"
	SensWorkspace  Sensitivity = "workspace"
	SensPersonal   Sensitivity = "personal"
	SensCredential Sensitivity = "credential"
	SensUnknown    Sensitivity = "unknown"
)

// Classifier registers the effect and data sensitivity of the built-in tools
// and derives both from the call target for path-based reads. Workspace and
// protected paths come from the caller: the profile's permission scope and
// the host's default protected locations.
type Classifier struct {
	Workspace []string
	Protected []string
}

// Classify maps one tool invocation to its registered effect and the
// sensitivity of the data it touches.
func (c Classifier) Classify(name, arguments string) (Effect, Sensitivity) {
	if server, _, ok := strings.Cut(name, "__"); ok && server != "" && server != "local" {
		return EffectExternal, SensUnknown
	}
	switch name {
	case "local__exec":
		return EffectExecute, SensUnknown
	case "local__tool_search":
		return EffectRead, SensWorkspace
	case "local__write_file", "local__edit_file":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(arguments), &args) != nil || args.Path == "" {
			return EffectWrite, SensUnknown
		}
		return EffectWrite, c.classifyPath(args.Path)
	case "local__current_time":
		return EffectPure, SensPublic
	case "local__search_worker_traces", "local__list_worker_traces", "local__get_worker_trace":
		return EffectRead, SensUnknown
	case "local__record_observation":
		return EffectPure, SensUnknown
	case "local__load_skill":
		// Skill bodies are user-installed instruction files on this machine.
		return EffectRead, SensWorkspace
	case "local__memory_save":
		// The saved content derives from the session context, whose
		// sensitivity cannot be decided statically; unknown keeps the write
		// out of automatic execution everywhere.
		return EffectWrite, SensUnknown
	case "local__update_plan", "local__task_plan":
		// Plan state is host-local bookkeeping: nothing is read and nothing
		// leaves the machine.
		return EffectPure, SensPublic
	case "project__query":
		// Querying the conversation's bound project: user content that is
		// already part of this conversation's working material.
		return EffectRead, SensWorkspace
	case "project__edit":
		// Editing the bound project changes user creative work; it asks for
		// confirmation in every non-automatic mode.
		return EffectWrite, SensUnknown
	case "local__delegate_task":
		// Delegation starts an autonomous subtask that can run further
		// tools, and the passed materials' sensitivity cannot be decided
		// statically: every mode confirms it, and the parent's session
		// accumulates unknown scope so later remote sends stay guarded.
		return EffectExecute, SensUnknown
	case "local__read_file":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(arguments), &args) != nil || strings.TrimSpace(args.Path) == "" {
			return EffectRead, SensUnknown
		}
		return EffectRead, c.classifyPath(args.Path)
	default:
		return EffectUnknown, SensUnknown
	}
}

func (c Classifier) classifyPath(path string) Sensitivity {
	abs, err := filepath.Abs(path)
	if err != nil {
		return SensUnknown
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	} else {
		// Resolve the existing parent of a new file as well, so a symlinked
		// directory cannot hide a credential destination from classification.
		parent, tail := filepath.Dir(abs), filepath.Base(abs)
		for {
			if resolved, e := filepath.EvalSymlinks(parent); e == nil {
				abs = filepath.Join(resolved, tail)
				break
			}
			next := filepath.Dir(parent)
			if next == parent {
				abs = filepath.Clean(abs)
				break
			}
			tail = filepath.Join(filepath.Base(parent), tail)
			parent = next
		}
	}
	for _, root := range c.Protected {
		if root != "" && under(abs, root) {
			return SensCredential
		}
	}
	if isCredentialName(filepath.Base(abs)) {
		return SensCredential
	}
	for _, root := range c.Workspace {
		if root != "" && under(abs, root) {
			return SensWorkspace
		}
	}
	return SensUnknown
}

// under reports whether path equals or lives below root. Both sides are
// cleaned; symlinks inside the tree are the caller's concern via EvalSymlinks.
func under(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isCredentialName matches file names that are credentials by convention
// (key material, dotfiles carrying tokens). It is deliberately name-based:
// such files must never auto-execute as plain reads even outside the
// protected directories.
func isCredentialName(base string) bool {
	name := strings.ToLower(base)
	if name == ".netrc" || name == ".git-credentials" || strings.HasPrefix(name, ".env") {
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".pfx", ".p12", ".keystore"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	for _, marker := range []string{"id_rsa", "id_ed25519", "id_ecdsa", "credential", "password", "secret_"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

// DefaultProtectedPaths returns the locations that always classify as
// credential sources: the application's own data directory (it holds the
// database and backups) and the conventional credential stores under the
// user's home. Environment secrets have no file path; they are handled by
// the MCP process environment allowlist instead.
func DefaultProtectedPaths(dataDir string) []string {
	paths := []string{}
	if dataDir != "" {
		paths = append(paths, dataDir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, rel := range []string{".ssh", ".gnupg", ".aws", ".kube", ".config/gcloud", ".netrc", ".git-credentials"} {
			paths = append(paths, filepath.Join(home, rel))
		}
	}
	return paths
}
