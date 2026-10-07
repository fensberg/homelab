package hypervisorapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// answers is a hypervisor that answers each question from a table, and
// refuses whoever does not say who they are.
func answers(t *testing.T, table map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=who=secret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		body, ok := table[strings.TrimPrefix(r.URL.Path, "/nodes/hv0")]
		if !ok {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

var aHost = map[string]string{
	"/status": `{"data": {"memory": {"total": 68719476736, "used": 5}, "cpuinfo": {"cpus": 16, "cores": 8, "sockets": 1, "model": " Some Processor "}, "uptime": 9}}`,
	"/hardware/pci": `{"data": [
	  {"id": "0000:00:02.0", "class": "0x030000", "vendor_name": "A Vendor", "device_name": "A Display"},
	  {"id": "0000:01:00.0", "class": "0x030200", "vendor_name": "Another", "device_name": "A 3D Card"},
	  {"id": "0000:02:00.0", "class": "0x020000", "vendor_name": "A Vendor", "device_name": "A Network Card"}]}`,
	"/storage": `{"data": [
	  {"storage": "disks", "type": "zfspool", "total": 1000, "used": 10},
	  {"storage": "offline", "type": "nfs"}]}`,
	"/qemu": `{"data": [
	  {"vmid": 10200, "name": "a-name-with-the-site-in-it", "maxmem": 8, "cpus": 6},
	  {"vmid": 105, "name": "somebody-elses", "maxmem": 4, "cpus": 2},
	  {"vmid": 900, "name": "a-template", "maxmem": 2, "cpus": 1, "template": 1}]}`,
}

func TestAHostIsReadAsWhatItHasAndNothingThatMoves(t *testing.T) {
	srv := answers(t, aHost)
	host, err := Survey(srv.Client(), srv.URL, "hv0", "PVEAPIToken=who=secret")
	if err != nil {
		t.Fatal(err)
	}
	if host.MemoryBytes != 68719476736 || host.Cores != 16 || host.Sockets != 1 || host.CPUModel != "Some Processor" {
		t.Errorf("the host's memory and processors were read as %+v", host)
	}
	if len(host.GPUs) != 2 || host.GPUs[1].Device != "A 3D Card" {
		t.Errorf("the display devices were read as %+v; a network card is not one, and a 3D card with no screen on it is", host.GPUs)
	}
	if len(host.Datastores) != 1 || host.Datastores["disks"].TotalBytes != 1000 || host.Datastores["disks"].Type != "zfspool" {
		t.Errorf("the datastores were read as %+v; one that reports no size holds nothing that can be budgeted", host.Datastores)
	}
	if len(host.Machines) != 3 || host.Machines[0].ID != 105 || host.Machines[2].MemoryBytes != 8 {
		t.Errorf("the machines were read as %+v; every one on the host, in id order", host.Machines)
	}
	if host.Machines[0].Template || !host.Machines[1].Template {
		t.Errorf("which machines are templates was read as %+v; a template is never started, and its memory is asked of nobody", host.Machines)
	}
}

func TestAHostWithNoDisplayDeviceHasNoneAndSaysSo(t *testing.T) {
	without := map[string]string{}
	for k, v := range aHost {
		without[k] = v
	}
	without["/hardware/pci"] = `{"data": [{"class": "0x020000", "vendor_name": "A Vendor", "device_name": "A Network Card"}]}`
	srv := answers(t, without)
	host, err := Survey(srv.Client(), srv.URL, "hv0", "PVEAPIToken=who=secret")
	if err != nil {
		t.Fatal(err)
	}
	if host.GPUs == nil || len(host.GPUs) != 0 {
		t.Errorf("a host with no display device was read as %+v; it has an empty list, which is a fact", host.GPUs)
	}
}

// A question the host will not answer is not answered for it. A host that
// refuses to list its devices has not been found to have no GPU.
func TestAQuestionTheHostWillNotAnswerIsARefusal(t *testing.T) {
	for _, missing := range []string{"/status", "/hardware/pci", "/storage", "/qemu"} {
		partial := map[string]string{}
		for k, v := range aHost {
			if k != missing {
				partial[k] = v
			}
		}
		srv := answers(t, partial)
		if _, err := Survey(srv.Client(), srv.URL, "hv0", "PVEAPIToken=who=secret"); err == nil || !strings.Contains(err.Error(), strings.Split(missing, "?")[0]) {
			t.Errorf("a host that refuses %s was read anyway, or the refusal does not name it: %v", missing, err)
		}
	}
	srv := answers(t, aHost)
	if _, err := Survey(srv.Client(), srv.URL, "hv0", "PVEAPIToken=someone=else"); err == nil {
		t.Error("a host that does not know who is asking was read anyway")
	}
	nothing := map[string]string{}
	for k, v := range aHost {
		nothing[k] = v
	}
	nothing["/status"] = `{"data": {"memory": {"total": 0}, "cpuinfo": {"cpus": 0}}}`
	srv = answers(t, nothing)
	if _, err := Survey(srv.Client(), srv.URL, "hv0", "PVEAPIToken=who=secret"); err == nil {
		t.Error("a host reporting no memory and no processors was taken as one to size against")
	}
	garbled := map[string]string{}
	for k, v := range aHost {
		garbled[k] = v
	}
	garbled["/qemu"] = `not json`
	srv = answers(t, garbled)
	if _, err := Survey(srv.Client(), srv.URL, "hv0", "PVEAPIToken=who=secret"); err == nil {
		t.Error("an answer that is not one was read as an empty host")
	}
}
