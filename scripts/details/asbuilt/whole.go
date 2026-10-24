package asbuilt

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WholeInputs is a root planned whole and from nothing: every file under it,
// the modules beside it included, with a stand-in for every value its
// template takes from the vault.
type WholeInputs struct {
	// Root is the root's directory.
	Root string
	// Files is every tracked file under Root, relative to it.
	Files []string
	// Work is where the root is copied and planned: a directory at the
	// root's own depth, so its references up the tree still resolve.
	Work string
	// Template is the root's config template, as the vault would fill it.
	Template []byte
	// Providers is where the root's providers are kept between plans, by
	// the lock file that chose them. Empty, they are fetched every time.
	Providers string
}

// wholeConfig is the stand-in config's name in the copy, and wholeBackend
// the file that gives the copy a local backend in place of the root's own.
const (
	wholeConfig  = "unbuilt.config.json"
	wholeBackend = "zz_unbuilt_backend_override.tf"
)

// PlanWhole plans a root from nothing, offline: its own files and every
// module beside it, a local backend in place of its own (an override file,
// which is OpenTofu's way of replacing a block without editing the file),
// and its template filled with a stand-in for every vault value. It is how
// a root that keeps its modules beside it, and so cannot be copied file by
// file as a site's roots are, is planned with no credential. A root
// planned so reads nothing of its vendors (var.offline).
func PlanWhole(in WholeInputs, tofu Tofu) ([]byte, error) {
	if depth(in.Root) != depth(in.Work) {
		return nil, fmt.Errorf("the copy of %s must be at its depth, so its references up the tree still resolve; %s is not", in.Root, in.Work)
	}
	for _, rel := range in.Files {
		b, err := os.ReadFile(filepath.Join(in.Root, rel))
		if err != nil {
			return nil, err
		}
		dst := filepath.Join(in.Work, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(in.Work, wholeBackend), []byte("terraform {\n  backend \"local\" {}\n}\n"), 0o600); err != nil {
		return nil, err
	}
	tpl, config, err := ConfigAgainst(in.Template, map[string]any{})
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(in.Work, wholeConfig)
	if err := writeJSON(configPath, config); err != nil {
		return nil, err
	}

	init := []string{"init", "-input=false", "-no-color"}
	kept := ""
	if in.Providers != "" {
		lock, err := os.ReadFile(filepath.Join(in.Root, ProviderLock))
		if err != nil {
			return nil, fmt.Errorf("%s has no provider lock to keep its providers by: %w", in.Root, err)
		}
		kept = filepath.Join(in.Providers, filepath.Base(in.Root), fmt.Sprintf("%x", sha256.Sum256(lock)))
		if info, err := os.Stat(kept); err == nil && info.IsDir() {
			init = append(init, "-plugin-dir="+kept)
			kept = ""
		}
	}
	env := withoutSignIns(OfflineEnv(os.Environ(), "", configPath), in.Work)
	if _, stderr, err := tofu(in.Work, env, init...); err != nil {
		return nil, fmt.Errorf("initialising the plan (%v):\n%s", err, ErrorSummary(stderr))
	}
	if kept != "" {
		if err := os.CopyFS(kept, os.DirFS(filepath.Join(in.Work, ".terraform", "providers"))); err != nil {
			return nil, fmt.Errorf("keeping %s's providers: %w", in.Root, err)
		}
	}
	if _, stderr, err := tofu(in.Work, env, "plan", "-input=false", "-lock=false", "-no-color", "-refresh=false", "-out=whole.tfplan"); err != nil {
		return nil, fmt.Errorf("the plan would be refused (%v):\n%s", err, ErrorSummary(stderr))
	}
	plan, _, err := tofu(in.Work, env, "show", "-json", "whole.tfplan")
	if err != nil {
		return nil, fmt.Errorf("reading the plan back: %w", err)
	}
	keyed, err := KeyedByAVaultValue(plan, tpl, config)
	if err != nil {
		return nil, err
	}
	if len(keyed) > 0 {
		return nil, &VaultKeyError{Keyed: keyed}
	}
	return plan, nil
}

// withoutSignIns is an environment with no way to sign in to source
// control: no token in a variable, and the GitHub CLI's configuration an
// empty directory. The GitHub provider falls back to whatever the CLI is
// signed in as, so a plan from nothing on a machine signed in would ask
// GitHub about a stand-in organisation with that machine's token, and fail
// or pass by whose machine it is.
func withoutSignIns(env []string, work string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if name == "GITHUB_TOKEN" || name == "GH_TOKEN" || name == "GH_CONFIG_DIR" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GH_CONFIG_DIR="+filepath.Join(work, ".no-sign-in"))
}
