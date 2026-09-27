package config

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
)

// DeclaredSites is every key of the config template's sites map, sorted. The
// template, not the rendered config: which sites exist is structure, needs no
// vault to answer, and is what the deploy workflow's matrix already reads.
func DeclaredSites(templatePath string) ([]string, error) {
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, err
	}
	var tpl struct {
		Sites map[string]json.RawMessage `json:"sites"`
	}
	if err := json.Unmarshal(raw, &tpl); err != nil {
		return nil, fmt.Errorf("the config template is not JSON: %w", err)
	}
	sites := make([]string, 0, len(tpl.Sites))
	for k := range tpl.Sites {
		sites = append(sites, k)
	}
	sort.Strings(sites)
	if len(sites) == 0 {
		return nil, fmt.Errorf("the config template at %s declares no site", templatePath)
	}
	return sites, nil
}

// ResolveSite says which site a command acts on.
//
// A site that was named must be declared. One that was not is the only site
// the template declares, and it is an error to leave it unnamed once there
// are two. There is deliberately no default: "site0 unless told otherwise"
// quietly aims every command that forgot to name a site at the first one, and
// the day a second site exists nothing says which commands did.
func ResolveSite(named, templatePath string) (string, error) {
	sites, err := DeclaredSites(templatePath)
	if err != nil {
		return "", err
	}
	if named != "" {
		if !slices.Contains(sites, named) {
			return "", fmt.Errorf("site %q is not declared; the config declares %s", named, strings.Join(sites, ", "))
		}
		return named, nil
	}
	if len(sites) > 1 {
		return "", fmt.Errorf("name the site: the config declares %s, and a command that acts on one of them has to say which", strings.Join(sites, ", "))
	}
	return sites[0], nil
}
