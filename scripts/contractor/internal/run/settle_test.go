package run

import (
	"os"
	"path/filepath"
	"testing"
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
	if err := os.MkdirAll(ctx.ClusterDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SettleMoves(ctx); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(filepath.Join(ctx.ClusterDir, "calls"))
	if want := "state list\nstate rm data.talos_cluster_health.this\n"; string(calls) != want {
		t.Errorf("the converge asked tofu:\n%s\nwant:\n%s", calls, want)
	}
}
