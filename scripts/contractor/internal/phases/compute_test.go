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
	got := datastoreFileURL(probeNode, "local-iso:iso/talos-v1.13.8-nocloud-amd64.iso")

	if strings.HasSuffix(got, "/local-iso:iso/talos-v1.13.8-nocloud-amd64.iso") {
		t.Fatal("the volume id was not escaped; its slash splits the path segment")
	}
	want := "https://192.0.2.10:8006/api2/json/nodes/hv-01/storage/local-iso/content/" +
		"local-iso:iso%2Ftalos-v1.13.8-nocloud-amd64.iso"
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
