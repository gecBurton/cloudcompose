package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadEnvironment_DispatchesOnTarget mirrors environment.py's
// load_environment: dispatching to the right cloud-specific loader based
// on the declared target field.
func TestLoadEnvironment_DispatchesOnTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		yaml     string
		wantType string
	}{
		{
			name:     "aws",
			yaml:     "target: aws\nname: prod\nvpc_id: vpc-1\npublic_subnets: [s1]\nprivate_subnets: [s2]\necs_cluster_arn: arn:aws:ecs:x\n",
			wantType: "*models.AwsEnvironment",
		},
		{
			name:     "azure",
			yaml:     "target: azure\nname: prod\ncontainer_apps_environment_name: env\nlog_analytics_workspace_id: x\nvnet_id: y\ninfrastructure_subnet_id: z\n",
			wantType: "*models.AzureEnvironment",
		},
		{
			name:     "gcp",
			yaml:     "target: gcp\nname: prod\nproject_id: my-project\n",
			wantType: "*models.GcpEnvironment",
		},
		{
			// No target declared -> defaults to aws, matching
			// environment.py's DEFAULT_TARGET = "aws".
			name:     "default-to-aws",
			yaml:     "name: prod\nvpc_id: vpc-1\npublic_subnets: [s1]\nprivate_subnets: [s2]\necs_cluster_arn: arn:aws:ecs:x\n",
			wantType: "*models.AwsEnvironment",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "env.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0644); err != nil {
				t.Fatalf("write failed: %v", err)
			}

			env, err := LoadEnvironment(path)
			if err != nil {
				t.Fatalf("LoadEnvironment failed: %v", err)
			}
			gotType := fmt.Sprintf("%T", env)
			if gotType != tc.wantType {
				t.Errorf("got type %s, want %s", gotType, tc.wantType)
			}
		})
	}
}

// TestLoadEnvironment_RejectsUnsupportedTarget mirrors load_environment's
// error path for a declared target outside {aws, azure, gcp}.
func TestLoadEnvironment_RejectsUnsupportedTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "env.yaml")
	if err := os.WriteFile(path, []byte("target: openstack\nname: prod\n"), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	_, err := LoadEnvironment(path)
	if err == nil {
		t.Fatal("expected an error for an unsupported target")
	}
}
