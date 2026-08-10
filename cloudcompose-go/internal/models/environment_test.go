package models

import "testing"

// TestAwsEnvironmentValidate_RejectsAlbWithoutSecurityGroup checks that a
// load balancer named without its security group is rejected: it leaves
// tasks with no way to accept its traffic except opening the port to the
// whole VPC.
func TestAwsEnvironmentValidate_RejectsAlbWithoutSecurityGroup(t *testing.T) {
	t.Parallel()
	env := NewAwsEnvironment()
	albArn := "arn:aws:lb:us-east-1:123456789012:loadbalancer/app/shared-alb/123"
	env.AlbArn = &albArn
	// AlbSecurityGroupID deliberately left nil.

	if err := env.Validate(); err == nil {
		t.Fatal("expected an error when alb_arn is set without alb_security_group_id")
	}
}

// TestAwsEnvironmentValidate_AcceptsNoAlbFields checks that an environment
// with no ALB configuration at all (a valid, ALB-less setup) is not
// rejected by the same check.
func TestAwsEnvironmentValidate_AcceptsNoAlbFields(t *testing.T) {
	t.Parallel()
	env := NewAwsEnvironment()

	if err := env.Validate(); err != nil {
		t.Fatalf("expected no error for an environment with no ALB fields, got %v", err)
	}
}

// TestAwsEnvironmentValidate_AcceptsAlbWithSecurityGroup checks the
// complementary positive case: both fields set together is valid.
func TestAwsEnvironmentValidate_AcceptsAlbWithSecurityGroup(t *testing.T) {
	t.Parallel()
	env := NewAwsEnvironment()
	albArn := "arn:aws:lb:us-east-1:123456789012:loadbalancer/app/shared-alb/123"
	sg := "sg-alb0123456789"
	env.AlbArn = &albArn
	env.AlbSecurityGroupID = &sg

	if err := env.Validate(); err != nil {
		t.Fatalf("expected no error when both alb_arn and alb_security_group_id are set, got %v", err)
	}
}

// TestServiceValidate_DatabaseMustCarryAName checks that a
// database-capability service with no database_name is rejected -- the
// normalizer is supposed to derive one from the compose file, and no
// backend should have to guess.
func TestServiceValidate_DatabaseMustCarryAName(t *testing.T) {
	t.Parallel()
	service := Service{Name: "db", Capability: CapabilityDatabase}

	if err := service.Validate(); err == nil {
		t.Fatal("expected an error for a database service with no database_name")
	}
}

// TestServiceValidate_ContainerNeedsNoDatabaseName checks that a plain
// container service is valid with no database_name at all.
func TestServiceValidate_ContainerNeedsNoDatabaseName(t *testing.T) {
	t.Parallel()
	service := Service{Name: "web", Capability: CapabilityContainer}

	if err := service.Validate(); err != nil {
		t.Fatalf("expected no error for a container service with no database_name, got %v", err)
	}
}

// TestServiceValidate_DatabaseWithNameIsValid checks the positive case: a
// database service that does carry a name passes validation.
func TestServiceValidate_DatabaseWithNameIsValid(t *testing.T) {
	t.Parallel()
	name := "app_db"
	service := Service{Name: "db", Capability: CapabilityDatabase, DatabaseName: &name}

	if err := service.Validate(); err != nil {
		t.Fatalf("expected no error for a database service with a database_name, got %v", err)
	}
}

// TestNewAwsEnvironment_Defaults pins the field defaults NewAwsEnvironment
// sets -- previously verified only implicitly (every golden example takes
// these defaults for granted), never pinned as their own assertion.
func TestNewAwsEnvironment_Defaults(t *testing.T) {
	t.Parallel()
	env := NewAwsEnvironment()

	if !env.RetainDataOnDestroy {
		t.Error("RetainDataOnDestroy default = false, want true")
	}
	if env.LogRetentionDays != 7 {
		t.Errorf("LogRetentionDays default = %d, want 7", env.LogRetentionDays)
	}
	if env.Target != "aws" {
		t.Errorf("Target default = %q, want aws", env.Target)
	}
	if env.Region != "us-east-1" {
		t.Errorf("Region default = %q, want us-east-1", env.Region)
	}
}
