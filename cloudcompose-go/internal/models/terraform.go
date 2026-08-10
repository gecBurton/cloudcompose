package models

// TerraformManifest is the root structure of a main.tf.json file.
// "resource" is left as map[string]any rather than a fixed AWSResources so
// the same manifest shape can serve Azure/GCP generators too.
//
// Resource deliberately has no `omitempty`: it is always present in the
// output -- even an application with zero AWS resources produces
// "resource": {}. Every other field is genuinely optional and keeps its
// own omitempty here.
type TerraformManifest struct {
	Terraform map[string]any `json:"terraform,omitempty"`
	Provider  map[string]any `json:"provider,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Variable  map[string]any `json:"variable,omitempty"`
	Resource  map[string]any `json:"resource"`
	Output    map[string]any `json:"output,omitempty"`
}
