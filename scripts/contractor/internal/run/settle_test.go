package run

import (
	"os"
	"path/filepath"
	"testing"

	"homelab/contractor/config"
)

// A converge forgets cached reads against the estate's own state - with the
// lock, since that state is shared - before it settles anything. A read left
// at an address the code no longer uses refuses every targeted apply that
// follows, which is how #555 would have halted the next converge.
func TestAConvergeForgetsCachedReadsFirst(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$*\" >> calls\n[ \"$1 $2\" = 'state list' ] && printf 'data.talos_cluster_health.this\\nproxmox_vm.cp\\n'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tofu"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx := NewContext(t.TempDir(), "site0")
	if err := os.MkdirAll(ctx.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ForgetCachedReads(ctx); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(filepath.Join(ctx.Dir, "calls"))
	if want := "state list\nstate rm data.talos_cluster_health.this\n"; string(calls) != want {
		t.Errorf("the converge asked tofu:\n%s\nwant:\n%s", calls, want)
	}
}

// A site has two roots, in the order they are applied, and In runs tofu in
// one of them without changing the context it was asked on.
func TestASiteHasTwoRootsAndInRunsInOne(t *testing.T) {
	ctx := NewContext("/repo", "site0")
	roots := ctx.Roots()
	if len(roots) != 2 || roots[0].Name != config.ClusterRoot || roots[1].Name != config.PlatformRoot {
		t.Fatalf("roots are %+v", roots)
	}
	if ctx.Dir != roots[0].Dir {
		t.Errorf("tofu runs in %s unless asked, not the cluster root", ctx.Dir)
	}
	for _, r := range roots {
		if r.Dir != filepath.Join("/repo", "management", r.Name) || filepath.Dir(r.LocalState) != r.Dir || filepath.Dir(r.BackendPgOn) != r.Dir {
			t.Errorf("the %s root's files are not its own: %+v", r.Name, r)
		}
	}
	in := ctx.In(ctx.Platform)
	if in.Dir != ctx.Platform.Dir || in.LocalState != ctx.Platform.LocalState || in.Site != "site0" {
		t.Errorf("In gave %+v", in.Root)
	}
	if ctx.Dir != ctx.Cluster.Dir {
		t.Error("In changed the context it was asked on")
	}
}

// A rename the configuration declares is settled by an apply that can make
// and destroy nothing, aimed at the two addresses of the rename; a
// configuration with no rename asks tofu for nothing at all.
func TestARenameIsSettledByAnApplyThatCanBuildNothing(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$*\" >> calls\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tofu"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx := NewContext(t.TempDir(), "site0")
	if err := os.MkdirAll(ctx.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SettleMoves(ctx); err != nil {
		t.Fatal(err)
	}
	if calls, _ := os.ReadFile(filepath.Join(ctx.Dir, "calls")); len(calls) != 0 {
		t.Errorf("a configuration with no rename asked tofu:\n%s", calls)
	}

	if err := os.WriteFile(filepath.Join(ctx.Dir, "renamed.tf"), []byte("moved {\n  from = thing.old\n  to   = thing.new\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SettleMoves(ctx); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(filepath.Join(ctx.Dir, "calls"))
	if want := "apply -refresh-only -auto-approve -input=false -json -target=thing.new -target=thing.old\n"; string(calls) != want {
		t.Errorf("the rename was settled with:\n%s\nwant:\n%s", calls, want)
	}
}

// An apply is aimed at what it is told to aim at, and replaces only what it
// is told to replace: an apply told nothing to replace replaces nothing.
func TestAnApplyReplacesOnlyWhatItIsTold(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$*\" >> calls\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tofu"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx := NewContext(t.TempDir(), "site0")
	if err := os.MkdirAll(ctx.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := TofuApplyReplacing(ctx, "an apply", []string{`a.config["k"]`}, "a.config"); err != nil {
		t.Fatal(err)
	}
	if err := TofuApply(ctx, "an apply", "a.machine"); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(filepath.Join(ctx.Dir, "calls"))
	want := "apply -input=false -auto-approve -json -target=a.config -replace=a.config[\"k\"]\n" +
		"apply -input=false -auto-approve -json -target=a.machine\n"
	if string(calls) != want {
		t.Errorf("tofu was asked:\n%s\nwant:\n%s", calls, want)
	}
}
