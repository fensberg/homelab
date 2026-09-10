package phases

import (
	"strings"
	"testing"

	"homelab/contractor/internal/config"
)

var probeNode = config.Node{Hostname: "hv-01", IP: "192.0.2.10"}

func TestDatastoreContentURL(t *testing.T) {
	got := datastoreContentURL(probeNode)
	want := "https://192.0.2.10:8006/api2/json/nodes/hv-01/storage/local-iso/content"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// A Proxmox volume id carries a colon and a slash and occupies one path
// segment. Left raw the slash splits the segment and the DELETE addresses a
// URL that does not exist - which fails as a 501 rather than as anything
// naming the real problem, so it is worth pinning.
func TestDatastoreFileURL_EscapesTheVolumeID(t *testing.T) {
	got := datastoreFileURL(probeNode, "local-iso:iso/talos-v1.13.9.iso")

	if strings.HasSuffix(got, "/local-iso:iso/talos-v1.13.9.iso") {
		t.Fatal("the volume id was not escaped; its slash splits the path segment")
	}
	want := "https://192.0.2.10:8006/api2/json/nodes/hv-01/storage/local-iso/content/" +
		"local-iso:iso%2Ftalos-v1.13.9.iso"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// The colon is legal in a path segment and Proxmox expects it literally, so
// escaping it would be as wrong as leaving the slash alone.
func TestDatastoreFileURL_KeepsTheColonLiteral(t *testing.T) {
	got := datastoreFileURL(probeNode, "local-iso:iso/x.iso")
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
		cluster = "local-iso:iso/talos-v1.13.9.iso"
		dmz     = "local-iso:iso/dmz-v1.13.9.iso"
	)

	for _, c := range []struct {
		volID  string
		prefix string
		want   bool
		why    string
	}{
		{cluster, clusterImagePrefix, true, "the cluster image is its own"},
		{dmz, dmzImagePrefix, true, "the untrusted image is its own"},
		{dmz, clusterImagePrefix, false, "the untrusted image must not answer for the cluster"},
		{cluster, dmzImagePrefix, false, "the cluster image must not answer for the untrusted zone"},
		// An orphan from an older run carries an older version, and finding it
		// is the whole reason the version is not spelled out.
		{"local-iso:iso/talos-v1.12.0.iso", clusterImagePrefix, true, "an older cluster image is still a cluster image"},
		{"local-iso:iso/dmz-v1.12.0.iso", dmzImagePrefix, true, "an older untrusted image is still one"},
		{"local-iso:iso/debian-12.iso", clusterImagePrefix, false, "somebody else's ISO is not ours to delete"},
	} {
		if got := strings.HasPrefix(c.volID, c.prefix); got != c.want {
			t.Errorf("%s: %q against prefix %q gave %v, want %v", c.why, c.volID, c.prefix, got, c.want)
		}
	}
}
