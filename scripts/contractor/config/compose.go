package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"homelab/details/applications"
)

// The config template names no application.
//
// What an application's secrets are made from is the application's to say:
// its declaration lists the fields of its item in the site's vault
// (homelab/details/applications). Which applications a site runs is the
// site's directory in the Flux tree. So the references to an application's
// vault item are not written in config/management.tpl.json, where removing
// the application would mean editing a shared file. They are composed: the
// committed template, plus - under the site being served - one entry per
// application the site was given, holding the environment it runs it in and a
// reference for each vault field its secrets read.
//
// The composed template is what every phase reads as the template: it is
// what `op inject` renders, what the vault check resolves, and what says
// which values of a plan are vault values. It holds references and no value,
// like the one it was composed from.

// ComposedTemplateFile is where the composed template is written, beside the
// committed one. Never committed: it is derived, on every run.
const ComposedTemplateFile = "management.composed.tpl.json"

// ApplicationRef is where one field of an application's vault item is: the
// site's own vault, the item named for the application.
func ApplicationRef(site, application, field string) string {
	return "op://" + site + "/" + application + "/" + field
}

// SiteApplications is the applications a site was given, each with its
// declaration. An application given to a site and not declared is an error:
// the site would be told to run something nothing here knows how to provide
// for.
func SiteApplications(repoRoot, site string) (map[string]applications.Application, []applications.Assignment, error) {
	assigned, err := applications.Assigned(repoRoot, site)
	if err != nil {
		return nil, nil, err
	}
	declared, err := applications.Read(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	byName := map[string]applications.Application{}
	for _, a := range declared {
		byName[a.Name] = a
	}
	out := map[string]applications.Application{}
	for _, as := range assigned {
		a, ok := byName[as.Application]
		if !ok {
			return nil, nil, fmt.Errorf("%s gives the site %s, and there is no application of that name under %s", applications.SiteFilePath(site), as.Application, applications.Dir)
		}
		out[as.Application] = a
	}
	return out, assigned, nil
}

// ComposeTemplate writes the template for one site - the committed template
// with the site's applications added - and returns where it wrote it.
func ComposeTemplate(repoRoot, templatePath, site string) (string, error) {
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		return "", err
	}
	composed, err := Compose(raw, repoRoot, site)
	if err != nil {
		return "", err
	}
	out := filepath.Join(filepath.Dir(templatePath), ComposedTemplateFile)
	if err := os.WriteFile(out, composed, 0o600); err != nil {
		return "", err
	}
	return out, nil
}

// Compose is the committed template's text with one site's applications
// added, and the committed text itself for a site that was given none.
func Compose(raw []byte, repoRoot, site string) ([]byte, error) {
	var tpl map[string]any
	if err := json.Unmarshal(raw, &tpl); err != nil {
		return nil, fmt.Errorf("the config template is not JSON: %w", err)
	}
	declared, assigned, err := SiteApplications(repoRoot, site)
	if err != nil {
		return nil, err
	}
	if len(assigned) == 0 {
		return raw, nil
	}
	sites, _ := tpl["sites"].(map[string]any)
	entry, ok := sites[site].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the config template declares no site %s, and it was given applications", site)
	}
	if _, written := entry["applications"]; written {
		return nil, fmt.Errorf("the config template writes sites.%s.applications itself; that is composed from %s and what each application declares, so the template names no application", site, applications.SiteFilePath(site))
	}
	apps := map[string]any{}
	for _, as := range assigned {
		vault := map[string]any{}
		for _, field := range declared[as.Application].VaultFields() {
			vault[field] = "{{ " + ApplicationRef(site, as.Application, field) + " }}"
		}
		apps[as.Application] = map[string]any{"environment": as.Environment, "vault": vault}
	}
	entry["applications"] = apps
	out, err := json.MarshalIndent(tpl, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
