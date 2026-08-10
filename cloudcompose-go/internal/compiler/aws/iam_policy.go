package aws

import (
	"encoding/json"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
)

// IAM policy document types shared across every AWS resource that embeds
// one as a Terraform attribute string (assume-role policies, resource
// policies, permission grants). AWS IAM does not care about JSON key
// order in a policy document -- these are plain typed structs marshalled
// with encoding/json, not ordered-map literals.
//
// Action/Resource are typed `any` because IAM itself accepts either a
// single string or a list of strings for both fields, and different
// policies in this codebase use both forms (a single "sts:AssumeRole"
// action, versus a list of ECR/S3 actions) -- this mirrors AWS's own
// schema flexibility, not an escape hatch.
type IAMPolicyDocument struct {
	Version   string               `json:"Version"`
	Statement []IAMPolicyStatement `json:"Statement"`
}

type IAMPolicyStatement struct {
	Effect    string         `json:"Effect,omitempty"`
	Action    any            `json:"Action,omitempty"`
	Principal any            `json:"Principal,omitempty"`
	Resource  any            `json:"Resource,omitempty"`
	Condition map[string]any `json:"Condition,omitempty"`
}

// newIAMPolicyDocument builds a single-statement IAM policy document, the
// shape every permission grant in this package needs.
func newIAMPolicyDocument(statement IAMPolicyStatement) IAMPolicyDocument {
	return IAMPolicyDocument{
		Version:   shared.IAMPolicyVersion,
		Statement: []IAMPolicyStatement{statement},
	}
}

// marshalJSONString renders v as a compact JSON string, for embedding in a
// Terraform attribute that itself holds JSON as a string (IAM policy
// documents, ECS container definitions, Secrets Manager secret values).
// Panics on error: every caller passes a value built from this package's
// own types, so a marshal failure here indicates a bug in this package,
// not bad input -- the same non-recoverable-error posture the rest of
// this package's inference functions already take (none of them return
// an error for a construction mistake they made themselves).
func marshalJSONString(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("marshalJSONString: " + err.Error())
	}
	return string(raw)
}
