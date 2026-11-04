package db

import (
	"context"
	"fmt"
	"sort"
)

// UngatedAppEntryPoint is a public service method or an enabled endpoint with no
// roles declared. Such an entry point used to be callable by any signed-in user
// and is now denied, so each one listed here needs an explicit role list before
// callers can reach it again.
type UngatedAppEntryPoint struct {
	App       string
	Namespace string
	Kind      string // "method" or "endpoint"
	Service   string // service name for methods, empty for endpoints
	Name      string
	Published bool // true when the gated definition is the published one
}

func (u UngatedAppEntryPoint) String() string {
	target := u.Name
	if u.Service != "" {
		target = u.Service + "." + u.Name
	}
	state := "draft"
	if u.Published {
		state = "published"
	}
	return fmt.Sprintf("%s (%s), %s %s, %s", u.App, u.Namespace, u.Kind, target, state)
}

// ListUngatedAppEntryPoints scans every active app's published and draft
// definitions and returns the entry points that declare no roles. Run it after
// upgrading to find the apps that now deny calls they used to allow.
func ListUngatedAppEntryPoints(ctx context.Context) ([]UngatedAppEntryPoint, error) {
	apps, err := ListActiveApps(ctx)
	if err != nil {
		return nil, err
	}
	return findUngatedAppEntryPoints(apps), nil
}

func findUngatedAppEntryPoints(apps []RegisteredApp) []UngatedAppEntryPoint {
	seen := map[UngatedAppEntryPoint]bool{}
	results := make([]UngatedAppEntryPoint, 0, 8)
	add := func(app RegisteredApp, definition *AppDefinition, published bool) {
		if definition == nil {
			return
		}
		for _, service := range definition.Services {
			for _, method := range service.Methods {
				if method.Visibility != "public" || len(declaredRoles(method.Roles)) > 0 {
					continue
				}
				finding := UngatedAppEntryPoint{
					App:       app.Name,
					Namespace: app.Namespace,
					Kind:      "method",
					Service:   service.Name,
					Name:      method.Name,
					Published: published,
				}
				if !seen[finding] {
					seen[finding] = true
					results = append(results, finding)
				}
			}
		}
		for _, endpoint := range definition.Endpoints {
			if !endpoint.Enabled || len(declaredRoles(endpoint.Roles)) > 0 {
				continue
			}
			finding := UngatedAppEntryPoint{
				App:       app.Name,
				Namespace: app.Namespace,
				Kind:      "endpoint",
				Name:      endpoint.Name,
				Published: published,
			}
			if !seen[finding] {
				seen[finding] = true
				results = append(results, finding)
			}
		}
	}
	for _, app := range apps {
		add(app, app.Definition, true)
		add(app, app.DraftDefinition, false)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].App != results[j].App {
			return results[i].App < results[j].App
		}
		if results[i].Kind != results[j].Kind {
			return results[i].Kind < results[j].Kind
		}
		return results[i].Name < results[j].Name
	})
	return results
}
