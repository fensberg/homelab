package repo

import (
	"sort"
	"testing"
	"time"

	"homelab/contractor/config"

	// The zone database, compiled in, so a zone is judged by the same
	// database wherever this runs rather than by what the machine has.
	_ "time/tzdata"
)

// Every site says which clock it keeps, by a name the zone database knows.
//
// What is done at an hour of a site's day - the machine roll's window, and
// for the first site the expediter's release - is that hour in the site's
// own zone, declared once in the config template. A site with no zone, or a
// misspelt one, would be rolled on whatever clock the runner keeps, which
// is UTC: the middle of the evening, with people on the site.
func TestEverySiteSaysWhichClockItKeeps(t *testing.T) {
	light(t)
	// The config's own types: the template is the config before the vault's
	// values are put in, and every value it holds outside the vault is
	// already of the type the config reads.
	var template config.Config
	repository(t).JSONInto(t, "config/management.tpl.json", &template)
	if len(template.Sites) == 0 {
		t.Fatal("the config template declares no sites, so no site's clock was checked")
	}
	var names []string
	for name := range template.Sites {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		zone := template.Sites[name].Timezone
		if zone == "" {
			t.Errorf("site %s does not say which clock it keeps: give it a timezone, by its name in the zone database", name)
			continue
		}
		if _, err := time.LoadLocation(zone); err != nil {
			t.Errorf("site %s keeps %q, which the zone database does not know: %v", name, zone, err)
		}
	}
}
