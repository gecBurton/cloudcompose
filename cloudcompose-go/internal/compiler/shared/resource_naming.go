package shared

// ResourceNamer returns a getName closure with the exact signature every
// cloud's own InferAWS/InferAzure/InferGcp (and their own FetchLogs/
// FetchStatus siblings) already thread through their own resource
// inference: given a resource's own local name (e.g. "sg", "cluster"),
// it returns the full, globally-unique name to give the actual cloud
// resource, by prefixing it with the environment's and app's own names.
//
// This was previously the identical one-line closure
// `func(resourceName string) string { return env.Name + "-" + app.Name +
// "-" + resourceName }` hand-copied into aws/infer.go, aws/logs.go,
// aws/status.go, azure/infer.go, azure/logs.go, azure/status.go, and
// gcp/infer.go -- the env.Name-app.Name-resourceName naming convention
// is a load-bearing cross-cutting contract (every generated cloud
// resource's name depends on it, and cloudcompose down/compile's own
// output-directory naming has to stay consistent with it -- see
// compile.go's own doc comment on that), so it's enforced here
// structurally rather than by comment/convention across 7 near-identical
// copies.
func ResourceNamer(envName, appName string) func(resourceName string) string {
	return func(resourceName string) string {
		return envName + "-" + appName + "-" + resourceName
	}
}
