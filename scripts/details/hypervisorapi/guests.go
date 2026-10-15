package hypervisorapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// CleanSnapshot is the name of the snapshot an untrusted worker is
// put back to: its disk as the converge left it. The converge takes it
// and the sweeper returns to it, so both need the one name.
const CleanSnapshot = "clean"

// SweeperTokenRefs is where a site's vault keeps the sweeper's token on
// its hypervisor, as the hypervisor phase stores it.
func SweeperTokenRefs(site string) (id, secret string) {
	item := "op://" + site + "/hypervisor/"
	return item + "sweeper_token_id", item + "sweeper_token_secret"
}

// Guest is one virtual machine as the hypervisor lists it to whoever asks:
// only the machines the asker's token may see.
type Guest struct {
	ID     int    `json:"vmid"`
	Name   string `json:"name"`
	Node   string `json:"node"`
	Status string `json:"status"`
}

// TaskPause is how long is left between two looks at a task that has not
// finished, and TaskLooks how many looks it is given.
var (
	TaskPause = 2 * time.Second
	TaskLooks = 90
)

// Guests is a session with a hypervisor's API for acting on its machines.
// Base is the API's address up to and including /api2/json, and the token
// is the one whose machines these are.
type Guests struct {
	Client      *http.Client
	Base        string
	TokenID     string
	TokenSecret string
}

func (g Guests) call(method, path string) (json.RawMessage, error) {
	req, err := http.NewRequest(method, g.Base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", g.TokenID, g.TokenSecret))
	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s: HTTP %d", method, path, resp.StatusCode)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%s %s: the answer is not the shape this reads: %w", method, path, err)
	}
	return envelope.Data, nil
}

// List is every machine the token may see.
func (g Guests) List() ([]Guest, error) {
	data, err := g.call(http.MethodGet, "/cluster/resources?type=vm")
	if err != nil {
		return nil, err
	}
	var out []Guest
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("the hypervisor's list of machines is not the shape this reads: %w", err)
	}
	return out, nil
}

// done starts a task and waits for the hypervisor to say it finished well.
// A task that was accepted is not a task that worked: the hypervisor
// answers at once with the task's name and does the work afterwards.
func (g Guests) done(what string, m Guest, method, path string) error {
	data, err := g.call(method, fmt.Sprintf("/nodes/%s/qemu/%d%s", m.Node, m.ID, path))
	if err != nil {
		return fmt.Errorf("%s machine %d: %w", what, m.ID, err)
	}
	var task string
	if err := json.Unmarshal(data, &task); err != nil || task == "" {
		return fmt.Errorf("%s machine %d: the hypervisor named no task to wait for", what, m.ID)
	}
	for look := 0; look < TaskLooks; look++ {
		data, err := g.call(http.MethodGet, fmt.Sprintf("/nodes/%s/tasks/%s/status", m.Node, url.PathEscape(task)))
		if err != nil {
			return fmt.Errorf("%s machine %d: %w", what, m.ID, err)
		}
		var status map[string]string
		if err := json.Unmarshal(data, &status); err != nil {
			// A task's status carries numbers beside its words; only the
			// words are read here.
			var loose map[string]any
			if err := json.Unmarshal(data, &loose); err != nil {
				return fmt.Errorf("%s machine %d: the task's status is not the shape this reads: %w", what, m.ID, err)
			}
			status = map[string]string{}
			for k, v := range loose {
				if word, ok := v.(string); ok {
					status[k] = word
				}
			}
		}
		if status["status"] == "stopped" {
			if ending := status["exitstatus"]; ending != "OK" {
				return fmt.Errorf("%s machine %d: the hypervisor's task ended %q", what, m.ID, ending)
			}
			return nil
		}
		time.Sleep(TaskPause)
	}
	return fmt.Errorf("%s machine %d: the hypervisor's task did not finish", what, m.ID)
}

// Stop cuts a machine's power. Not a shutdown: nothing the machine is
// running is asked, because what it is running is not trusted to answer.
func (g Guests) Stop(m Guest) error {
	return g.done("stopping", m, http.MethodPost, "/status/stop")
}

// Start powers a machine on.
func (g Guests) Start(m Guest) error {
	return g.done("starting", m, http.MethodPost, "/status/start")
}

// PutBack returns a stopped machine's disk to the clean snapshot. The
// hypervisor leaves the machine stopped unless it is asked to start it.
func (g Guests) PutBack(m Guest) error {
	return g.done("putting back", m, http.MethodPost, "/snapshot/"+CleanSnapshot+"/rollback")
}

// Delete removes a stopped machine and its disks.
func (g Guests) Delete(m Guest) error {
	return g.done("deleting", m, http.MethodDelete, "?purge=1&destroy-unreferenced-disks=1")
}

// Shutdown asks a machine to stop, and waits until it has. For a machine
// that is trusted to answer: one that has run nothing yet.
func (g Guests) Shutdown(m Guest) error {
	return g.done("shutting down", m, http.MethodPost, "/status/shutdown")
}

// HasClean says whether a machine has the clean snapshot.
func (g Guests) HasClean(m Guest) (bool, error) {
	data, err := g.call(http.MethodGet, fmt.Sprintf("/nodes/%s/qemu/%d/snapshot", m.Node, m.ID))
	if err != nil {
		return false, err
	}
	var held []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &held); err != nil {
		return false, fmt.Errorf("machine %d's snapshots are not the shape this reads: %w", m.ID, err)
	}
	for _, h := range held {
		if h.Name == CleanSnapshot {
			return true, nil
		}
	}
	return false, nil
}

// TakeClean takes the clean snapshot of a stopped machine: its disks, and
// nothing of what it was running.
func (g Guests) TakeClean(m Guest) error {
	return g.done("taking the clean snapshot of", m, http.MethodPost, "/snapshot?snapname="+CleanSnapshot)
}
