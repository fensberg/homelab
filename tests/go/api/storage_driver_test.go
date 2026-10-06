//go:build api

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"homelab/contractor/config"
	"homelab/details/hypervisorapi"
	"homelab/details/onepassword"
	"homelab/tests/harness"
)

// The storage driver's token reaches two paths on the hypervisor and no
// more, and a volume it makes outlives the machine it is attached to.
//
// The driver runs inside the cluster and holds a token for the hypervisor's
// API, so what that token can do is what a compromise of the driver can do.
// The playbook confines it to the site's worker pool and a storage of its
// own (hypervisor-prep.yml, section 10). This is the half of that which a
// playbook cannot show: that Proxmox, asked with that token, actually refuses
// what lies outside the two paths.
//
// It also answers the question the whole design rests on and no document
// settles: when a machine is destroyed with one of the driver's volumes
// attached, the volume stays. If a Proxmox upgrade ever changes that, a
// machine's replacement starts taking the site's history with it, and this
// is what says so first.
//
// It makes two scratch machines - never started, one in the worker pool and
// one in no pool - and removes them, with ids far outside any site's band.
// No machine of the site's is touched.
// covers: api:hypervisor
func TestTheStorageDriversTokenReachesItsTwoPathsAndNoFurther(t *testing.T) {
	site := harness.SiteConfig(t)
	node := harness.FirstHypervisorNode(t)
	net := harness.SiteNetwork(t)
	pve := proxmox{t: t, base: fmt.Sprintf("https://%s:8006/api2/json", node.IP), node: node.Hostname}

	provisioner := fmt.Sprintf("PVEAPIToken=%s=%s", site.Hypervisor.TokenID, site.Hypervisor.TokenSecret)
	idRef, secretRef := hypervisorapi.StorageDriverTokenRefs(harness.Site())
	driverID, err := onepassword.Read(idRef)
	require.NoError(t, err, "the vault holds no token for the storage driver. The hypervisor phase makes it: task configure-hypervisor SITE=%s", harness.Site())
	driverSecret, err := onepassword.Read(secretRef)
	require.NoError(t, err, "the vault holds the storage driver's token id and not its secret")
	driver := fmt.Sprintf("PVEAPIToken=%s=%s", driverID, driverSecret)

	// Far above the band any site's machines are numbered in, and the
	// site's own, so two sites on one hypervisor do not collide here either.
	inPool, outside := 990000+site.Octet*10+1, 990000+site.Octet*10+2
	volumes, machines := config.VolumeStorage(harness.Site()), node.Datastores.Disks
	volume := hypervisorapi.DriverVolume("pvc-confinement-trial")
	volid := volumes + ":" + volume

	for _, id := range []int{inPool, outside} {
		status, _ := pve.call(http.MethodGet, fmt.Sprintf("/nodes/%s/qemu/%d/status/current", pve.node, id), provisioner, nil)
		require.NotEqual(t, http.StatusOK, status,
			"a machine with the scratch id %d is already there, left by a run of this test that did not finish. Look at it, remove it, and run again", id)
	}
	t.Cleanup(func() {
		for _, id := range []int{inPool, outside} {
			pve.settle(pve.call(http.MethodDelete, fmt.Sprintf("/nodes/%s/qemu/%d", pve.node, id), provisioner, url.Values{"purge": {"1"}}))
		}
		pve.call(http.MethodDelete, fmt.Sprintf("/nodes/%s/storage/%s/content/%s", pve.node, volumes, volid), provisioner, nil)
	})

	// Two machines that are never started: one a worker as far as the
	// hypervisor's access list can tell, one nobody's. The second has a disk
	// of its own on the machines' datastore, to be what the driver must not
	// be able to delete.
	pve.must(http.MethodPost, "/nodes/"+pve.node+"/qemu", provisioner, url.Values{
		"vmid": {fmt.Sprint(inPool)}, "name": {"storage-driver-trial-in-pool"}, "memory": {"16"}, "pool": {net.WorkerPool()},
	}, "making the scratch machine in the worker pool")
	pve.must(http.MethodPost, "/nodes/"+pve.node+"/qemu", provisioner, url.Values{
		"vmid": {fmt.Sprint(outside)}, "name": {"storage-driver-trial-outside"}, "memory": {"16"}, "virtio0": {machines + ":1"},
	}, "making the scratch machine outside every pool")

	t.Run("it makes a volume in its own storage", func(t *testing.T) {
		pve.t = t
		pve.must(http.MethodPost, fmt.Sprintf("/nodes/%s/storage/%s/content", pve.node, volumes), driver, url.Values{
			"vmid": {hypervisorapi.DriverVolumeOwner}, "filename": {volume}, "size": {"1G"},
		}, "the driver's token could not make a volume in the storage that is its own")
	})

	t.Run("it attaches that volume to a machine in the worker pool", func(t *testing.T) {
		pve.t = t
		pve.must(http.MethodPut, fmt.Sprintf("/nodes/%s/qemu/%d/config", pve.node, inPool), driver, url.Values{"scsi1": {volid}},
			"the driver's token could not attach its volume to a machine in the worker pool, which is the one thing it is for")
	})

	t.Run("it is refused a machine outside the worker pool", func(t *testing.T) {
		pve.t = t
		status, body := pve.call(http.MethodPut, fmt.Sprintf("/nodes/%s/qemu/%d/config", pve.node, outside), driver, url.Values{"scsi1": {volid}})
		require.Equal(t, http.StatusForbidden, status,
			"the driver's token changed a machine outside the worker pool (%s). It reaches further than the pool it was given", body)
	})

	t.Run("it is refused another machine's disk on the machines' datastore", func(t *testing.T) {
		pve.t = t
		theirs := fmt.Sprintf("%s:vm-%d-disk-0", machines, outside)
		status, body := pve.call(http.MethodDelete, fmt.Sprintf("/nodes/%s/storage/%s/content/%s", pve.node, machines, theirs), driver, nil)
		require.Equal(t, http.StatusForbidden, status,
			"the driver's token deleted, or was not refused, a disk on the datastore every machine's own disks are on (%s)", body)
		status, body = pve.call(http.MethodGet, fmt.Sprintf("/nodes/%s/storage/%s/content", pve.node, machines), provisioner, nil)
		require.Equal(t, http.StatusOK, status, "listing the machines' datastore: %s", body)
		require.Contains(t, string(body), theirs, "the other machine's disk is gone from the machines' datastore")
	})

	t.Run("its volume outlives the machine it was attached to", func(t *testing.T) {
		pve.t = t
		// As the provider destroys a machine: purged, and with every disk
		// that belongs to it removed whether its config names it or not.
		pve.must(http.MethodDelete, fmt.Sprintf("/nodes/%s/qemu/%d", pve.node, inPool), provisioner,
			url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}}, "destroying the scratch machine with the volume attached")
		status, body := pve.call(http.MethodGet, fmt.Sprintf("/nodes/%s/storage/%s/content", pve.node, volumes), driver, nil)
		require.Equal(t, http.StatusOK, status, "the driver's token could not list its own storage: %s", body)
		require.Contains(t, string(body), volid,
			"the volume went with the machine. A worker's replacement would take whatever the driver keeps for the site, so nothing may be moved onto it")
	})

	t.Run("it deletes its own volume", func(t *testing.T) {
		pve.t = t
		pve.must(http.MethodDelete, fmt.Sprintf("/nodes/%s/storage/%s/content/%s", pve.node, volumes, volid), driver, nil,
			"the driver's token could not delete a volume in its own storage")
	})
}

// proxmox is one node's API, asked as whoever the caller says.
type proxmox struct {
	t          *testing.T
	base, node string
}

// call asks once and answers with the status and the body. A DELETE's
// parameters go in the query; anything else's in the form.
func (p proxmox) call(method, path, auth string, params url.Values) (int, []byte) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	target, body := p.base+path, io.Reader(nil)
	if len(params) > 0 {
		if method == http.MethodDelete || method == http.MethodGet {
			target += "?" + params.Encode()
		} else {
			body = strings.NewReader(params.Encode())
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	require.NoError(p.t, err, "building the request")
	req.Header.Set("Authorization", auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := harness.HypervisorClient(p.t, 60*time.Second).Do(req)
	require.NoError(p.t, err, "the hypervisor API is unreachable from this runner")
	defer resp.Body.Close()
	answer, err := io.ReadAll(resp.Body)
	require.NoError(p.t, err, "reading the response body")
	return resp.StatusCode, answer
}

// must asks, requires a success, and waits for the task the answer names
// when it names one.
func (p proxmox) must(method, path, auth string, params url.Values, doing string) {
	p.t.Helper()
	status, body := p.call(method, path, auth, params)
	require.Equal(p.t, http.StatusOK, status, "%s: %s", doing, body)
	require.True(p.t, p.settle(status, body), "%s: the task it started did not end well", doing)
}

// settle waits for the task an answer names, and says whether it ended OK.
// An answer that names no task has nothing to wait for. The task is watched
// with the provisioning token whoever started it, because that token may
// read every task on the node and the driver's may not.
func (p proxmox) settle(status int, body []byte) bool {
	p.t.Helper()
	var answer struct {
		Data any `json:"data"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &answer) != nil {
		return false
	}
	upid, ok := answer.Data.(string)
	if !ok || !strings.HasPrefix(upid, "UPID:") {
		return true
	}
	site := harness.SiteConfig(p.t)
	watcher := fmt.Sprintf("PVEAPIToken=%s=%s", site.Hypervisor.TokenID, site.Hypervisor.TokenSecret)
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		_, state := p.call(http.MethodGet, fmt.Sprintf("/nodes/%s/tasks/%s/status", p.node, url.PathEscape(upid)), watcher, nil)
		var task struct {
			Data struct {
				Status     string `json:"status"`
				ExitStatus string `json:"exitstatus"`
			} `json:"data"`
		}
		if json.Unmarshal(state, &task) == nil && task.Data.Status == "stopped" {
			return task.Data.ExitStatus == "OK"
		}
	}
	return false
}
