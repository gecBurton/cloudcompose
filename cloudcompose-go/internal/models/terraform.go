package models

// TerraformManifest is the root structure of a main.tf.json file.
// "resource" is left as map[string]any so the same manifest shape can
// serve Azure/GCP generators too. Resource has no `omitempty`: it is
// always present in the output, even as "resource": {}.
type TerraformManifest struct {
	Terraform map[string]any `json:"terraform,omitempty"`
	Provider  map[string]any `json:"provider,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Variable  map[string]any `json:"variable,omitempty"`
	Resource  map[string]any `json:"resource"`
	Output    map[string]any `json:"output,omitempty"`
}
