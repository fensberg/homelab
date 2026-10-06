package phases

import (
	"strings"
	"testing"

	"homelab/contractor/config"
)

var probeNode = config.Node{Hostname: "hv-01", IP: "192.0.2.10", Datastores: config.Datastores{Disks: "tank-disks", Images: "tank-images"}}

// The node's own image datastore, not one the code assumes every host has.
func TestDatastoreContentURL(t *testing.T) {
	got := datastoreContentURL(probeNode)
	want := "https://192.0.2.10:8006/api2/json/nodes/hv-01/storage/tank-images/content"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// A Proxmox volume id carries a colon and a slash and occupies one path
// segment. Left raw the slash splits the segment and the DELETE addresses a
// URL that does not exist - which fails as a 501 rather than as anything
// naming the real problem, so it is worth pinning.
func TestDatastoreFileURL_EscapesTheVolumeID(t *testing.T) {
	got := datastoreFileURL(probeNode, "tank-images:iso/talos-v1.13.9.iso")

	if strings.HasSuffix(got, "/tank-images:iso/talos-v1.13.9.iso") {
		t.Fatal("the volume id was not escaped; its slash splits the path segment")
	}
	want := "https://192.0.2.10:8006/api2/json/nodes/hv-01/storage/tank-images/content/" +
		"tank-images:iso%2Ftalos-v1.13.9.iso"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// The colon is legal in a path segment and Proxmox expects it literally, so
// escaping it would be as wrong as leaving the slash alone.
func TestDatastoreFileURL_KeepsTheColonLiteral(t *testing.T) {
	got := datastoreFileURL(probeNode, "tank-images:iso/x.iso")
	if strings.Contains(got, "%3A") {
		t.Errorf("the colon was percent-encoded, which Proxmox does not expect: %s", got)
	}
}

// The two images share a datastore, and nothing may confuse them.
//
// They are told apart by prefix rather than by a qualifier on the end because
// every one of these ends in ".iso": a suffix test for the cluster image would
// match the untrusted one too. Adopting or deleting the wrong one would put the
// overlay-carrying image under the machine whose entire purpose is not to have
// it, and nothing downstream would report the swap - the file name at the
// datastore path would be the one that was asked for either way.
func TestTheClusterAndUntrustedImagesAreNeverConfused(t *testing.T) {
	const (
		cluster = "tank-images:iso/talos-v1.13.9.iso"
		dmz     = "tank-images:iso/dmz-v1.13.9.iso"
	)

	for _, c := range []struct {
		volID string
		image string
		want  bool
		why   string
	}{
		{cluster, clusterImage, true, "the cluster image is its own"},
		{dmz, dmzImage, true, "the untrusted image is its own"},
		{dmz, clusterImage, false, "the untrusted image must not answer for the cluster"},
		{cluster, dmzImage, false, "the cluster image must not answer for the untrusted zone"},
		// An orphan from an older run carries an older version, and finding it
		// is the whole reason the version is not spelled out.
		{"tank-images:iso/talos-v1.12.0.iso", clusterImage, true, "an older cluster image is still a cluster image"},
		{"tank-images:iso/dmz-v1.12.0.iso", dmzImage, true, "an older untrusted image is still one"},
		{"tank-images:iso/debian-12.iso", clusterImage, false, "somebody else's ISO is not ours to delete"},
		// The same image in another datastore is not the one this node keeps.
		{"tank-disks:iso/talos-v1.13.9.iso", clusterImage, false, "an image in another datastore is not this node's"},
	} {
		prefix := imagePrefix(probeNode, c.image)
		if got := strings.HasPrefix(c.volID, prefix); got != c.want {
			t.Errorf("%s: %q against prefix %q gave %v, want %v", c.why, c.volID, prefix, got, c.want)
		}
	}
}

// The hypervisor's API is asked nothing by a client that could not tell it
// from an impostor. With no authority to verify it against, the question is
// not sent: it used to be, with verification off, and the API token with it.
func TestTheHypervisorIsNotAskedAnythingWithoutItsAuthority(t *testing.T) {
	node := config.Node{Hostname: "a-node", IP: "192.0.2.10", Datastores: config.Datastores{Images: "a-datastore"}}
	for name, authority := range map[string]string{"none": "", "not one": "operator"} {
		hv := config.Hypervisor{TokenID: "an-id", TokenSecret: "a-secret", Authority: authority}
		if _, err := listDatastoreVolumes(hv, node); err == nil || !strings.Contains(err.Error(), "authority") {
			t.Errorf("with %s for an authority, the datastore was asked: %v", name, err)
		}
		if err := deleteDatastoreFile(hv, node, "a-datastore:iso/an-image.img"); err == nil || !strings.Contains(err.Error(), "authority") {
			t.Errorf("with %s for an authority, a delete was sent: %v", name, err)
		}
	}
}
