package shared

// ResourceNamer returns a closure that builds the full, globally-unique
// name for a cloud resource by prefixing its local name (e.g. "sg",
// "cluster") with the environment's and app's names.
func ResourceNamer(envName, appName string) func(resourceName string) string {
	return func(resourceName string) string {
		return envName + "-" + appName + "-" + resourceName
	}
}
