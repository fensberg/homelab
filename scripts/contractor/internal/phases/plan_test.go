package phases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A plan holds every attribute of every resource it touches, and this
// repository keeps hostnames, addresses and credentials out of git on purpose.
// The repository is also public, which makes an Actions job summary and a pull
// request comment world-readable. So the summary this produces reports
// structure only - addresses and actions, never a value - which is the same
// line check-inventory already draws between "the reference resolves" and "here is
// what it resolved to".
const planWithSecrets = `{
  "format_version": "1.2",
  "resource_changes": [
    {"address":"proxmox_virtual_environment_vm.talos_cp[3]","type":"proxmox_virtual_environment_vm",
     "change":{"actions":["create"],"before":null,
       "after":{"name":"example-cp-04","ipv4_addresses":["192.0.2.103"],"description":"secret-value-here"}}},
    {"address":"proxmox_virtual_environment_vm.talos_cp[4]","type":"proxmox_virtual_environment_vm",
     "change":{"actions":["create"],"before":null,
       "after":{"name":"example-cp-05"}}},
    {"address":"talos_machine_configuration_apply.control_plane[0]","type":"talos_machine_configuration_apply",
     "change":{"actions":["update"],"before":{"x":"old-token"},"after":{"x":"new-token"}}},
    {"address":"tailscale_tailnet_key.this","type":"tailscale_tailnet_key",
     "change":{"actions":["delete","create"],"before":{"key":"tskey-abc"},"after":{"key":"tskey-def"}}},
    {"address":"kubernetes_secret.state_db_credentials","type":"kubernetes_secret",
     "change":{"actions":["no-op"],"before":{"p":"hunter2"},"after":{"p":"hunter2"}}}
  ]
}`

func TestSummarisePlan_ReportsStructure(t *testing.T) {
	got, err := summarisePlan([]byte(planWithSecrets))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"proxmox_virtual_environment_vm.talos_cp[3]",
		"proxmox_virtual_environment_vm.talos_cp[4]",
		"talos_machine_configuration_apply.control_plane[0]",
		"tailscale_tailnet_key.this",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary omits the address %q; a reviewer cannot tell what is changing", want)
		}
	}
	// no-op resources are noise in a review, not information.
	if strings.Contains(got, "kubernetes_secret.state_db_credentials") {
		t.Error("summary lists a no-op resource; the point is to show what changes")
	}
}

// The property this whole design exists for.
func TestSummarisePlan_NeverLeaksAValue(t *testing.T) {
	got, err := summarisePlan([]byte(planWithSecrets))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, secret := range []string{
		"example-cp-04", "example-cp-05", // a site name
		"10.10.10.103", // addressing
		"secret-value-here", "old-token", "new-token",
		"tskey-abc", "tskey-def", "hunter2",
	} {
		if strings.Contains(got, secret) {
			t.Errorf("summary contains the value %q. This output is posted to a public pull request; it must report structure only", secret)
		}
	}
}

func TestSummarisePlan_CountsByAction(t *testing.T) {
	got, err := summarisePlan([]byte(planWithSecrets))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 create, 1 update, 1 replace (delete+create counts once, as a replace).
	for _, want := range []string{"2 to add", "1 to change", "1 to replace"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q; got:\n%s", want, got)
		}
	}
}

// A destroy is the line a reviewer must not skim past.
func TestSummarisePlan_CallsOutDestroys(t *testing.T) {
	const destroying = `{"format_version":"1.2","resource_changes":[
	  {"address":"proxmox_virtual_environment_vm.talos_cp[4]","change":{"actions":["delete"],"before":{"n":"x"},"after":null}}]}`
	got, err := summarisePlan([]byte(destroying))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "1 to destroy") {
		t.Fatalf("a destroy was not counted; got:\n%s", got)
	}
	if !strings.Contains(strings.ToUpper(got), "DESTROY") {
		t.Error("a plan that destroys does not say so prominently; that is the one line a reviewer must not skim past")
	}
}

// An empty plan is a real and common answer, and saying "no changes" is more
// useful than an empty table.
func TestSummarisePlan_NoChanges(t *testing.T) {
	got, err := summarisePlan([]byte(`{"format_version":"1.2","resource_changes":[],"output_changes":{}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(got), "no changes") {
		t.Errorf("an empty plan should say so plainly; got:\n%s", got)
	}
}

func TestSummarisePlan_RejectsGarbage(t *testing.T) {
	if _, err := summarisePlan([]byte("not json")); err == nil {
		t.Error("expected an error for output that is not a plan")
	}
}

// A resource address is not the safe half of a plan.
//
// `for_each` over the config's hypervisor map keys the resource by the
// hypervisor's real name, so a plan prints a vault value in an address without
// printing any attribute at all. That reached a public Actions log and a pull
// request comment, because everything watching for leaks was watching values.
func TestForEachKeysThatCanCarryANameAreRedacted(t *testing.T) {
	got := redactKeys(`proxmox_download_file.talos_disk_image["node0"]`)
	if strings.Contains(got, "some-hostname") {
		t.Errorf("a name-shaped for_each key survived redaction: %s", got)
	}
	if !strings.Contains(got, "proxmox_download_file.talos_disk_image") {
		t.Errorf("redaction destroyed the address as well as the key: %s", got)
	}
}

// Numeric keys stay. They come from octets and node numbering, carry no proper
// noun, and are the difference between "a control-plane VM is being replaced"
// and knowing which one - which is what a reviewer needs when a plan destroys.
func TestNumericKeysAreKept(t *testing.T) {
	got := redactKeys(`proxmox_virtual_environment_vm.talos_cp["100"]`)
	if got != `proxmox_virtual_environment_vm.talos_cp["100"]` {
		t.Errorf("a numeric key was redacted, losing which resource changed: %s", got)
	}
}

// An address with no key is untouched.
func TestUnkeyedAddressesAreUntouched(t *testing.T) {
	const addr = "tailscale_tailnet_key.hypervisor"
	if got := redactKeys(addr); got != addr {
		t.Errorf("redactKeys mangled an unkeyed address: %s", got)
	}
}

// The comment is deliberately plain: a heading naming the site, the change,
// and nothing else.
//
// The copy it replaced greeted the reader, signed itself, and restated what
// the output was - "Addresses and actions only, never a value" - which the
// artefact then contradicted by containing both a value and the whole run. A
// reader can see what the output is; the comment's job is to carry the change.
func TestTheCommentIsJustTheChange(t *testing.T) {
	body := commentBody("site0", "  replace  tailscale_tailnet_key.hypervisor", "abc1234")

	for _, unwanted := range []string{
		"contractor checking in", // a signature
		"never a value",          // a claim the body cannot keep on its own
		"This is how the estate", // a restatement of what the reader can see
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the comment still carries %q", unwanted)
		}
	}

	if !strings.Contains(body, "## Plan — site0") {
		t.Error("the comment does not say which site it is about, which matters " +
			"the moment a pull request plans more than one")
	}
	if !strings.Contains(body, "tailscale_tailnet_key.hypervisor") {
		t.Error("the comment does not contain the change, which is its only job")
	}
}

// A later run must be able to find and replace this comment rather than adding
// another. The workflow posted a new one every time, so a pull request that
// planned three times carried three comments and the reader had to work out
// which was current.
func TestTheCommentCarriesAMarkerPerSite(t *testing.T) {
	a := commentBody("site0", "x", "abc1234")
	b := commentBody("site10", "x", "abc1234")

	if !strings.HasPrefix(a, commentMarker("site0")) {
		t.Error("the comment has no marker, so a later run cannot find it to update")
	}
	if commentMarker("site0") == commentMarker("site10") {
		t.Error("two sites share a marker, so planning both on one pull request " +
			"would have each overwrite the other")
	}
	if strings.Contains(b, commentMarker("site0")) {
		t.Error("a site's comment carries another site's marker")
	}
}

// A plan comment must say which commit it describes.
//
// A later run replaces the comment in place, so its contents are only ever as
// current as the last plan that succeeded. When the plan lane cannot run - no
// runner, which is this estate's normal state while it is being rebuilt - the
// previous comment stays, describing a change that is no longer the one being
// proposed.
//
// Push 3 -> 5, then 5 -> 17, and with no plan in between a reviewer reads
// "2 to add" and approves fourteen machines. Nothing distinguished a fresh
// comment from a stale one: no commit, no timestamp, and GitHub dismisses
// stale reviews on a push but never comments.
func TestTheCommentSaysWhichCommitItDescribes(t *testing.T) {
	body := commentBody("site0", "add  proxmox_virtual_environment_vm.talos_cp[3]", "abc1234")
	if !strings.Contains(body, "abc1234") {
		t.Fatalf("the comment does not name the commit it describes, so a stale one is "+
			"indistinguishable from a current one:\n%s", body)
	}
}

// The marker has to stay first. The workflow finds the comment to replace with
// startsWith(marker), so anything printed before it turns every re-plan into a
// new comment - which is the pile-up that made the reader work out which was
// current, and would now leave several plans each claiming a different commit.
func TestTheMarkerStaysTheFirstThingInTheComment(t *testing.T) {
	body := commentBody("site0", "x", "abc1234")
	if !strings.HasPrefix(body, commentMarker("site0")) {
		t.Fatalf("the comment no longer starts with its marker, so the workflow cannot "+
			"find and replace it:\n%s", body)
	}
}

// A commit that cannot be determined is left out rather than guessed at. A
// comment with no provenance is worse than one with it; a comment carrying the
// wrong commit is worse than both, because it looks like evidence.
func TestAnUnknownCommitIsOmittedRatherThanInvented(t *testing.T) {
	body := commentBody("site0", "x", "")
	if strings.Contains(body, "Planned against") {
		t.Errorf("the comment claims provenance it does not have:\n%s", body)
	}
	if !strings.HasPrefix(body, commentMarker("site0")) {
		t.Error("the marker moved when the commit was absent")
	}
}

// --- the change a plan could not see (#320) ---------------------------------

// An output changing IS a change to the estate, and used to summarise as
// "No changes. The estate already matches the config."
//
// This is the regression that mattered most, because of which sentence it
// produced. The plan comment is the last thing read before a merge, and a
// reviewer who is not a code expert relies on it - so a plan that reports
// calm when the config moved does not merely fail to inform, it supplies
// confidence. `talosconfig` is the exact output that did it: a credential,
// changed by a commit, reported as nothing.
func TestSummarisePlan_SeesAnOutputOnlyChange(t *testing.T) {
	const outputsOnly = `{"format_version":"1.2","resource_changes":[],
	  "output_changes":{"talosconfig":{"actions":["update"],"before":"a","after":"b",
	                                   "before_sensitive":true,"after_sensitive":true}}}`
	got, err := summarisePlan([]byte(outputsOnly))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToLower(got), "no changes") {
		t.Fatalf("a plan that rewrites a credential reported no changes; got:\n%s", got)
	}
	if !strings.Contains(got, "output.talosconfig") {
		t.Errorf("the changed output is not named; got:\n%s", got)
	}
	if !strings.Contains(got, "1 output(s) to change") {
		t.Errorf("the output change is not counted; got:\n%s", got)
	}
}

// An output can BE a secret - talosconfig and kubeconfig both are - so the
// rule that governs resource attributes governs these with more force rather
// than less. Names and verbs; never a before or an after.
func TestSummarisePlan_NeverLeaksAnOutputValue(t *testing.T) {
	const secretOutput = `{"format_version":"1.2","resource_changes":[],
	  "output_changes":{"talosconfig":{"actions":["update"],
	    "before":"tskey-old-abc","after":"tskey-new-def"}}}`
	got, err := summarisePlan([]byte(secretOutput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, secret := range []string{"tskey-old-abc", "tskey-new-def"} {
		if strings.Contains(got, secret) {
			t.Errorf("an output value reached a world-readable comment: %q\n%s", secret, got)
		}
	}
}

// An output change must not inflate the works count. A reviewer scans that
// line for the destroy number, and folding unrelated things into it is how a
// number stops meaning what it says.
func TestSummarisePlan_KeepsOutputsOutOfTheWorksCount(t *testing.T) {
	const mixed = `{"format_version":"1.2",
	  "resource_changes":[{"address":"proxmox_virtual_environment_vm.talos_cp[0]","mode":"managed",
	                       "change":{"actions":["delete"]}}],
	  "output_changes":{"talosconfig":{"actions":["update"]},"kubeconfig":{"actions":["update"]}}}`
	got, err := summarisePlan([]byte(mixed))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "0 to add, 0 to change, 0 to replace, 1 to destroy") {
		t.Errorf("the works count is wrong or has changed shape; got:\n%s", got)
	}
	if !strings.Contains(got, "2 output(s) to change") {
		t.Errorf("the outputs are not counted separately; got:\n%s", got)
	}
}

// A data source is measured, not built. It gets the verb this estate already
// uses for reading the ground as it is rather than trusting the drawings, and
// it stays out of the works count - `read` is tofu's word for the mechanism,
// and this comment is read by somebody deciding whether to merge.
func TestSummarisePlan_CallsADataSourceReadASurvey(t *testing.T) {
	const reading = `{"format_version":"1.2","resource_changes":[
	  {"address":"data.talos_client_configuration.this","mode":"data","change":{"actions":["read"]}}]}`
	got, err := summarisePlan([]byte(reading))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToLower(got), "no changes") {
		t.Fatalf("a deferred data source read was dropped as noise; got:\n%s", got)
	}
	if !strings.Contains(got, "survey") {
		t.Errorf("a data source read is not named as a survey; got:\n%s", got)
	}
	if !strings.Contains(got, "1 to survey") {
		t.Errorf("the survey is not counted; got:\n%s", got)
	}
	if strings.Contains(got, "1 to change") || strings.Contains(got, "1 to add") {
		t.Errorf("a survey was counted as a work; got:\n%s", got)
	}
}

// "Nothing changed" and "this is not a plan" must not produce the same
// sentence. There is deliberately no third, calmer status for the second: a
// plan that examined nothing means the tool is broken, and the honest response
// is to fail rather than to find a reassuring way to say it.
func TestSummarisePlan_RefusesADocumentThatIsNotAPlan(t *testing.T) {
	for _, tc := range []struct{ name, doc string }{
		{"an empty object", `{}`},
		{"a plausible shape with no format version", `{"resource_changes":[]}`},
		{"some other tool's JSON", `{"results":[],"version":"3"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := summarisePlan([]byte(tc.doc))
			if err == nil {
				t.Fatalf("accepted a non-plan and summarised it as:\n%s", got)
			}
			if strings.Contains(strings.ToLower(err.Error()), "no changes") {
				t.Error("the refusal reads like a clean result")
			}
		})
	}
}

// --- provenance a reader can actually look up (#320) ------------------------

// The SHA in the comment must be one that exists somewhere the reader can go.
//
// GitHub checks a pull request out at a merge commit it synthesises for the
// run. Planning against it is right; naming it is useless, because it is in no
// branch and in none of the pull request's own commits. The operator's words
// on being handed one: "I have no idea what ea516dc is."
func TestHeadFromEventNamesTheBranchHead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "event.json")
	const head = "28b3e08e1c4d5a6f7089badc0ffee1234567890a"
	if err := os.WriteFile(path, []byte(`{"pull_request":{"head":{"sha":"`+head+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := headFromEvent(path); got != head[:7] {
		t.Errorf("headFromEvent = %q, want the short branch head %q", got, head[:7])
	}
}

// Every failure here must produce nothing rather than something SHA-shaped.
// The bug this replaces was a fallback that quietly returned a plausible wrong
// answer, which is worse than returning none - a comment without provenance is
// worse than one with it, and far better than one carrying a commit that is
// wrong.
func TestHeadFromEventPrefersNothingToSomethingWrong(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct{ name, path string }{
		{"no event path at all", ""},
		{"a path that does not exist", filepath.Join(dir, "absent.json")},
		{"an event that is not JSON", write("bad.json", "not json")},
		{"a push event, which has no pull_request", write("push.json", `{"after":"abc"}`)},
		{"an empty sha", write("empty.json", `{"pull_request":{"head":{"sha":""}}}`)},
		{"a truncated sha", write("short.json", `{"pull_request":{"head":{"sha":"28b3e08"}}}`)},
		{"a sha that is not hex", write("hex.json", `{"pull_request":{"head":{"sha":"`+strings.Repeat("z", 40)+`"}}}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := headFromEvent(tc.path); got != "" {
				t.Errorf("headFromEvent = %q, want empty - a SHA-shaped string that resolves to nothing is the failure being prevented", got)
			}
		})
	}
}

// The comment has to say the plan covers the merge result, not the branch
// alone. Naming the branch head without that would be precise about the wrong
// thing: what was planned is the tree that exists after merging.
func TestCommentBodySaysThePlanCoversTheMerge(t *testing.T) {
	body := commentBody("site0", "  add  x", "28b3e08")
	if !strings.Contains(body, "28b3e08") {
		t.Fatalf("the commit is missing:\n%s", body)
	}
	if !strings.Contains(strings.ToLower(body), "merged") {
		t.Errorf(`the comment does not say the plan covers the merge result, so the SHA reads as "the branch was planned":
%s`, body)
	}
}
