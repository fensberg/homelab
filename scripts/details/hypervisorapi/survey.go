package hypervisorapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Host is what a hypervisor has, as it says so itself: the facts a site's
// capacity is worked out from. Read and never typed, so a second host with
// different hardware needs no line of the estate changed to be sized right.
//
// Only what does not move from one reading to the next. What is in use right
// now is a monitor's business; this is what there is.
type Host struct {
	// MemoryBytes is the host's memory, all of it.
	MemoryBytes int64 `json:"memory_bytes"`
	// Cores is how many processors the host schedules onto, counting each
	// thread; Sockets how many physical packages they are in.
	Cores    int    `json:"cores"`
	Sockets  int    `json:"sockets"`
	CPUModel string `json:"cpu_model"`
	// GPUs is every display device the host has. A host with none has an
	// empty list, which is a fact and not a gap.
	GPUs []Device `json:"gpus"`
	// Datastores is each storage the host knows that reports a size, by its
	// name.
	Datastores map[string]Datastore `json:"storages"`
	// Machines is every virtual machine on the host, the site's and anybody
	// else's, by id. Which are the site's is the address plan's to say; what
	// is left is memory the site cannot have.
	Machines []Machine `json:"machines"`
}

// Device is one device, as its vendor and its own name.
type Device struct {
	Vendor string `json:"vendor"`
	Device string `json:"device"`
}

// Datastore is one storage: what kind it is and how much it holds.
type Datastore struct {
	Type       string `json:"type"`
	TotalBytes int64  `json:"total_bytes"`
}

// Machine is one virtual machine: its id and what it is given. Not its
// name, which for a site's own machines carries the site's.
type Machine struct {
	ID          int   `json:"id"`
	MemoryBytes int64 `json:"memory_bytes"`
	Cores       int   `json:"cores"`
	// Template says it is a template: never started, so the memory it is
	// given on paper is asked of nobody.
	Template bool `json:"is_template"`
}

// displayClass is the PCI class every display controller is in: VGA, 3D and
// the rest. The API gives a class as hex with the subclass after it.
const displayClass = "0x03"

// Survey asks one node of a hypervisor what it has. base is the API's
// address up to and including /api2/json, and auth the whole Authorization
// header. Every question must be answered: a host that will not say what
// devices it has is not a host with no GPU.
func Survey(client *http.Client, base, node, auth string) (Host, error) {
	var host Host
	ask := func(path string, into any) error {
		req, err := http.NewRequest(http.MethodGet, base+"/nodes/"+node+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", auth)
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("asking the hypervisor for %s: %w", path, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("asking the hypervisor for %s: HTTP %d", path, resp.StatusCode)
		}
		envelope := struct {
			Data any `json:"data"`
		}{Data: into}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return fmt.Errorf("the hypervisor's answer for %s is not the shape this reads: %w", path, err)
		}
		return nil
	}

	var status struct {
		Memory struct {
			Total int64 `json:"total"`
		} `json:"memory"`
		CPUInfo struct {
			CPUs    int    `json:"cpus"`
			Sockets int    `json:"sockets"`
			Model   string `json:"model"`
		} `json:"cpuinfo"`
	}
	if err := ask("/status", &status); err != nil {
		return host, err
	}
	if status.Memory.Total <= 0 || status.CPUInfo.CPUs <= 0 {
		return host, fmt.Errorf("the hypervisor reports %d bytes of memory and %d processors, which is not a host anything can be sized against", status.Memory.Total, status.CPUInfo.CPUs)
	}
	host.MemoryBytes, host.Cores = status.Memory.Total, status.CPUInfo.CPUs
	host.Sockets, host.CPUModel = status.CPUInfo.Sockets, strings.TrimSpace(status.CPUInfo.Model)

	var devices []struct {
		Class  string `json:"class"`
		Vendor string `json:"vendor_name"`
		Device string `json:"device_name"`
	}
	if err := ask("/hardware/pci", &devices); err != nil {
		return host, err
	}
	host.GPUs = []Device{}
	for _, d := range devices {
		if strings.HasPrefix(strings.ToLower(d.Class), displayClass) {
			host.GPUs = append(host.GPUs, Device{Vendor: d.Vendor, Device: d.Device})
		}
	}

	var storages []struct {
		Storage string `json:"storage"`
		Type    string `json:"type"`
		Total   int64  `json:"total"`
	}
	if err := ask("/storage", &storages); err != nil {
		return host, err
	}
	host.Datastores = map[string]Datastore{}
	for _, s := range storages {
		if s.Total > 0 {
			host.Datastores[s.Storage] = Datastore{Type: s.Type, TotalBytes: s.Total}
		}
	}

	var machines []struct {
		VMID   int   `json:"vmid"`
		MaxMem int64 `json:"maxmem"`
		CPUs   int   `json:"cpus"`
		// As the API spells it; the decoder matches the field by name.
		Template int
	}
	if err := ask("/qemu", &machines); err != nil {
		return host, err
	}
	host.Machines = []Machine{}
	for _, m := range machines {
		host.Machines = append(host.Machines, Machine{ID: m.VMID, MemoryBytes: m.MaxMem, Cores: m.CPUs, Template: m.Template == 1})
	}
	sort.Slice(host.Machines, func(i, j int) bool { return host.Machines[i].ID < host.Machines[j].ID })
	return host, nil
}
