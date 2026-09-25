package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"homelab/details/console"
	"homelab/details/onepassword"
	"homelab/details/tofustate"
	"homelab/details/vaults"
)

// bucketType is the resource every site bucket is, in management/estate/site.
const bucketType = "cloudflare_r2_bucket"

// adoptBuckets takes into state every bucket the plan would create that the
// account already holds.
//
// Buckets outlive the estate's machinery: demolish-estate releases them rather
// than destroying them, because production holds the only copy of the world
// while no site stands. So a build finds them already there, and creating one
// that exists fails. The plan says which buckets the estate wants and under
// which addresses, which keeps the naming in the HCL alone rather than worked
// out again here.
func (e *estate) adoptBuckets(api cloudflare) error {
	plan := filepath.Join(e.dir, "adopt.tfplan")
	defer os.Remove(plan)
	if _, err := e.tofuOutput("plan", "-input=false", "-out="+plan); err != nil {
		return err
	}
	shown, err := e.tofuOutput("show", "-json", plan)
	if err != nil {
		return err
	}
	wanted, err := plannedCreates([]byte(shown), bucketType)
	if err != nil {
		return err
	}
	if len(wanted) == 0 {
		return nil
	}
	existing, err := api.bucketNames()
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(wanted) {
		if !existing[name] {
			continue
		}
		console.Info("adopting a bucket that outlived the last estate: " + wanted[name])
		if err := e.run("import", "-input=false", wanted[name], e.cfg.Access.AccountID+"/"+name+"/default"); err != nil {
			return err
		}
	}
	return nil
}

// plannedCreates maps the name of each resource of one type the plan would
// create to its address.
func plannedCreates(showJSON []byte, resourceType string) (map[string]string, error) {
	p, err := tofustate.ParsePlan(showJSON)
	if err != nil {
		return nil, fmt.Errorf("reading the plan: %w", err)
	}
	out := map[string]string{}
	for _, rc := range p.ResourceChanges {
		if rc.Type != resourceType || !rc.Creates() {
			continue
		}
		name, ok := rc.AfterString("name")
		if !ok || name == "" {
			return nil, fmt.Errorf("the plan creates %s without a known name, so it cannot be matched to a bucket that already exists", rc.Address)
		}
		out[name] = rc.Address
	}
	return out, nil
}

// releaseBuckets takes every bucket out of state before a demolish, so the
// destroy that follows cannot reach them. The next build adopts them back.
func (e *estate) releaseBuckets() error {
	listed, err := e.tofuOutput("state", "list")
	if err != nil {
		return err
	}
	for _, addr := range bucketAddresses(strings.Fields(listed)) {
		console.Info("releasing " + addr + ": buckets outlive the estate")
		if err := e.run("state", "rm", addr); err != nil {
			return err
		}
	}
	return nil
}

// bucketAddresses picks the buckets out of a state listing, in or out of a
// module.
func bucketAddresses(listed []string) []string {
	var out []string
	for _, a := range listed {
		if strings.HasPrefix(a, bucketType+".") || strings.Contains(a, "."+bucketType+".") {
			out = append(out, a)
		}
	}
	return out
}

// grant writes what the estate grants each site into that site's -shared
// vault, item by item, as the grants output of management/estate lays it out.
// Only fields whose value changed are written, and only their names are
// printed.
func (e *estate) grant() error {
	out, err := e.tofuOutput("output", "-json", "grants")
	if err != nil {
		return err
	}
	grants, err := grantsFrom([]byte(out))
	if err != nil {
		return err
	}
	for _, site := range sortedKeys(grants) {
		vault := vaults.SiteShared(site)
		for _, item := range sortedKeys(grants[site]) {
			changed, err := onepassword.WriteItem(vault, item, grants[site][item])
			if err != nil {
				return fmt.Errorf("granting %s its %s: %w", site, item, err)
			}
			if len(changed) == 0 {
				console.Ok(fmt.Sprintf("%s's %s grant is already current", site, item))
				continue
			}
			console.Ok(fmt.Sprintf("granted %s its %s: %s", site, item, strings.Join(changed, ", ")))
		}
	}
	return nil
}

// grantsFrom reads `tofu output -json grants`: site, then item, then field.
// Every value must be a string, because every one becomes a vault field; a
// grant that is not one is refused rather than written as something else.
func grantsFrom(out []byte) (map[string]map[string]map[string]string, error) {
	var raw map[string]map[string]map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("reading the estate's grants: %w", err)
	}
	grants := map[string]map[string]map[string]string{}
	for site, items := range raw {
		grants[site] = map[string]map[string]string{}
		for item, fields := range items {
			grants[site][item] = map[string]string{}
			for field, v := range fields {
				s, ok := v.(string)
				if !ok || s == "" {
					return nil, fmt.Errorf("the grant %s/%s/%s is not a value that can be written to a vault", site, item, field)
				}
				grants[site][item][field] = s
			}
		}
	}
	return grants, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
