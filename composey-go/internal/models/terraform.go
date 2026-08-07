package models

// TerraformManifest is the root structure of a main.tf.json file, mirroring
// composey/models/terraform.py. Unlike the Python model, "resource" here is
// left as map[string]any rather than a fixed AWSResources so the same
// manifest shape can eventually serve Azure/GCP generators too, once ported.
//
// Resource deliberately has no `omitempty`: Pydantic's `resource` field on
// TerraformManifest uses default_factory=AWSResources rather than
// Optional[...]=None, so it is always present in Python's output -- even an
// application with zero AWS resources produces "resource": {}. Every other
// field genuinely is Optional in Python and keeps its own omitempty here.
type TerraformManifest struct {
	Terraform map[string]any `json:"terraform,omitempty"`
	Provider  map[string]any `json:"provider,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Variable  map[string]any `json:"variable,omitempty"`
	Resource  map[string]any `json:"resource"`
	Output    map[string]any `json:"output,omitempty"`
}
