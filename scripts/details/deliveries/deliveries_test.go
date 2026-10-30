package deliveries

import (
	"slices"
	"testing"
)

func TestParseReadsEachSectionAndItsLines(t *testing.T) {
	got, err := Parse("# header prose\n\n# [pypi: a-b A_VERSION=1.2]\na-b==1.2 --hash=sha256:x\n# a comment inside\n# [end]\n\n# [fetch: tool TOOL_VERSION=3]\nhttps://example.invalid/tool --hash=sha256:y\n# [end]\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d sections, want 2: %+v", len(got), got)
	}
	a := got["pypi:a-b"]
	if a.VersionKey != "A_VERSION" || a.Version != "1.2" || !slices.Equal(a.Lines, []string{"a-b==1.2 --hash=sha256:x"}) {
		t.Errorf("the pypi section was read as %+v", a)
	}
	if f := got["fetch:tool"]; f.Kind != "fetch" || len(f.Lines) != 1 {
		t.Errorf("the fetch section was read as %+v", f)
	}
}

func TestParseRefusesAMalformedLock(t *testing.T) {
	for _, body := range []string{
		"# [end]\n",
		"# [pypi: a A_VERSION=1]\n# [pypi: b B_VERSION=1]\n",
		"# [pypi: a A_VERSION=1]\na==1 --hash=sha256:x\n# [end]\n# [pypi: a A_VERSION=1]\n# [end]\n",
		"# [pypi: a A_VERSION=1]\na==1\n",
	} {
		if _, err := Parse(body); err == nil {
			t.Errorf("want a refusal for:\n%s", body)
		}
	}
}
