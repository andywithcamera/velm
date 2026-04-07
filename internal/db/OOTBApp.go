package db

import "strings"

func isUnscopedSystemAppName(appName, namespace string) bool {
	return normalizeIdentifier(namespace) == "" && normalizeIdentifier(appName) == "system"
}

func isOOTBBaseAppName(appName, namespace string) bool {
	return normalizeIdentifier(namespace) == "" && normalizeIdentifier(appName) == "base"
}

func isOOTBAdminAppName(appName, namespace string) bool {
	return normalizeIdentifier(namespace) == "" && (normalizeIdentifier(appName) == "system" || normalizeIdentifier(appName) == "base")
}

func IsOOTBBaseApp(app RegisteredApp) bool {
	return isOOTBBaseAppName(app.Name, app.Namespace)
}

func appUsesOOTBLandingPage(app RegisteredApp) bool {
	return isOOTBAdminAppName(app.Name, app.Namespace)
}

func definitionOwnsPhysicalSchema(definition *AppDefinition) bool {
	if definition == nil {
		return false
	}
	if normalizeIdentifier(definition.Namespace) != "" {
		return true
	}
	return isOOTBBaseAppName(definition.Name, definition.Namespace)
}

func appOwnsPhysicalSchema(app RegisteredApp) bool {
	if normalizeIdentifier(app.Namespace) != "" {
		return true
	}
	return isOOTBBaseAppName(app.Name, app.Namespace)
}

func findExactRegisteredAppByNameOrNamespace(apps []RegisteredApp, rawName string) (RegisteredApp, bool) {
	name := normalizeIdentifier(rawName)
	if name == "" {
		return RegisteredApp{}, false
	}

	for _, app := range apps {
		if strings.EqualFold(app.Name, name) || strings.EqualFold(app.Namespace, name) {
			return app, true
		}
	}
	return RegisteredApp{}, false
}
