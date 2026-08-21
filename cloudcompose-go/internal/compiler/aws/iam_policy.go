package aws

import (
	"encoding/json"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
)

// IAMPolicyDocument is an IAM policy document, marshalled to JSON for
// embedding in a Terraform attribute string.
type IAMPolicyDocument struct {
	Version   string               `json:"Version"`
	Statement []IAMPolicyStatement `json:"Statement"`
}

// IAMPolicyStatement is a single statement in an IAM policy document.
// Action/Resource/Principal are `any` since IAM accepts either a single
// string or a list of strings.
type IAMPolicyStatement struct {
	Effect    string         `json:"Effect,omitempty"`
	Action    any            `json:"Action,omitempty"`
	Principal any            `json:"Principal,omitempty"`
	Resource  any            `json:"Resource,omitempty"`
	Condition map[string]any `json:"Condition,omitempty"`
}

// newIAMPolicyDocument builds a single-statement IAM policy document.
func newIAMPolicyDocument(statement IAMPolicyStatement) IAMPolicyDocument {
	return IAMPolicyDocument{
		Version:   shared.IAMPolicyVersion,
		Statement: []IAMPolicyStatement{statement},
	}
}

// marshalJSONString renders v as a compact JSON string, for embedding in a
// Terraform attribute that holds JSON as a string. Panics on error, since
// callers always pass values built from this package's own types.
func marshalJSONString(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("marshalJSONString: " + err.Error())
	}
	return string(raw)
}
