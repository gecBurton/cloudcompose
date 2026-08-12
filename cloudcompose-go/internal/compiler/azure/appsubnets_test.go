package azure

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// Tests for docs/azure-app-isolation-design.md: appSubnetsAzure creates
// each app's own Container Apps Environment and four delegated subnets,
// carved out of the environment's shared AppsCIDR at the app's own
// SubnetIndex.

func TestAppSubnetsAzure_CreatesFourSubnets(t *testing.T) {
	t.Parallel()
	app := &models.Application{Name: "myapp"}
	env := testAppEnv(0)
	resources := models.NewAzureResources()

	if err := appSubnetsAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil); err != nil {
		t.Fatalf("appSubnetsAzure failed: %v", err)
	}

	for _, key := range []string{"infrastructure", "postgresql", "mysql", "redis"} {
		if _, ok := resources.Subnet[key]; !ok {
			t.Errorf("expected a %q subnet", key)
		}
	}
}

func TestAppSubnetsAzure_SubnetIndexZeroCarvesFirstSlice(t *testing.T) {
	t.Parallel()
	app := &models.Application{Name: "myapp"}
	env := testAppEnv(0)
	resources := models.NewAzureResources()

	if err := appSubnetsAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil); err != nil {
		t.Fatalf("appSubnetsAzure failed: %v", err)
	}

	want := map[string]string{
		"infrastructure": "10.0.128.0/26",
		"postgresql":     "10.0.128.64/26",
		"mysql":          "10.0.128.128/26",
		"redis":          "10.0.128.192/26",
	}
	for key, wantCIDR := range want {
		subnet := resources.Subnet[key]
		if len(subnet.AddressPrefixes) != 1 || subnet.AddressPrefixes[0] != wantCIDR {
			t.Errorf("%s.AddressPrefixes = %v, want [%s]", key, subnet.AddressPrefixes, wantCIDR)
		}
	}
}

func TestAppSubnetsAzure_DistinctSubnetIndexesDoNotOverlap(t *testing.T) {
	t.Parallel()
	app := &models.Application{Name: "myapp"}

	seen := map[string]int{}
	for _, idx := range []int{0, 1, 5, 127} {
		env := testAppEnv(idx)
		resources := models.NewAzureResources()
		if err := appSubnetsAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil); err != nil {
			t.Fatalf("appSubnetsAzure(index=%d) failed: %v", idx, err)
		}
		for key, subnet := range resources.Subnet {
			cidr := subnet.AddressPrefixes[0]
			if prevIdx, exists := seen[cidr]; exists {
				t.Errorf("subnet-index %d's %s subnet (%s) collides with subnet-index %d's", idx, key, cidr, prevIdx)
			}
			seen[cidr] = idx
		}
	}
}

func TestAppSubnetsAzure_LastValidIndexFitsInAppsCIDR(t *testing.T) {
	t.Parallel()
	// docs/azure-app-isolation-design.md's own math: a /17 AppsCIDR
	// supports up to 128 apps (indexes 0-127) at /24 each. Index 127
	// must still succeed; this is the boundary the design's own claim
	// rests on, not just a mid-range sanity check.
	app := &models.Application{Name: "myapp"}
	env := testAppEnv(127)
	resources := models.NewAzureResources()
	if err := appSubnetsAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil); err != nil {
		t.Fatalf("appSubnetsAzure(index=127) failed: %v", err)
	}
	infra := resources.Subnet["infrastructure"]
	if infra.AddressPrefixes[0] != "10.0.255.0/26" {
		t.Errorf("infrastructure.AddressPrefixes = %v, want [10.0.255.0/26]", infra.AddressPrefixes)
	}
}

func TestAppSubnetsAzure_IndexBeyondCapacityErrors(t *testing.T) {
	t.Parallel()
	// Index 128 would need a 129th /24 slice of a /17 range that only
	// has 128 -- Cidrsubnet itself should reject this, and this
	// function should surface that as an error, not silently wrap
	// around or produce an invalid CIDR.
	app := &models.Application{Name: "myapp"}
	env := testAppEnv(128)
	resources := models.NewAzureResources()
	err := appSubnetsAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)
	if err == nil {
		t.Fatalf("expected an error for subnet-index 128 (beyond the /17 range's 128-app capacity)")
	}
}

func TestAppSubnetsAzure_InfrastructureSubnetIsDelegatedToContainerApps(t *testing.T) {
	t.Parallel()
	app := &models.Application{Name: "myapp"}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	if err := appSubnetsAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil); err != nil {
		t.Fatalf("appSubnetsAzure failed: %v", err)
	}
	infra := resources.Subnet["infrastructure"]
	if len(infra.Delegation) != 1 || infra.Delegation[0].ServiceDelegation[0].Name != "Microsoft.App/environments" {
		t.Errorf("infrastructure subnet delegation = %+v, want Microsoft.App/environments", infra.Delegation)
	}
}

func TestAppSubnetsAzure_RedisSubnetIsNotDelegated(t *testing.T) {
	t.Parallel()
	// azurerm_private_endpoint (Managed Redis) attaches to a plain
	// subnet, unlike the delegated subnets Flexible Server needs --
	// confirmed against the real schema, not assumed from symmetry
	// with the other three subnets.
	app := &models.Application{Name: "myapp"}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	if err := appSubnetsAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil); err != nil {
		t.Fatalf("appSubnetsAzure failed: %v", err)
	}
	redis := resources.Subnet["redis"]
	if len(redis.Delegation) != 0 {
		t.Errorf("redis subnet delegation = %+v, want none", redis.Delegation)
	}
}

func TestAppSubnetsAzure_CreatesContainerAppEnvironmentReferencingInfraSubnet(t *testing.T) {
	t.Parallel()
	app := &models.Application{Name: "myapp"}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	if err := appSubnetsAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil); err != nil {
		t.Fatalf("appSubnetsAzure failed: %v", err)
	}
	cae, ok := resources.ContainerAppEnvironment["main"]
	if !ok {
		t.Fatalf("expected a ContainerAppEnvironment")
	}
	if cae.InfrastructureSubnetID == nil || *cae.InfrastructureSubnetID != "${azurerm_subnet.infrastructure.id}" {
		t.Errorf("InfrastructureSubnetID = %v, want a reference to the infrastructure subnet", cae.InfrastructureSubnetID)
	}
}

func TestAppSubnetsAzure_SetsSubnetIDFieldsOnEnvironment(t *testing.T) {
	t.Parallel()
	// The whole point of this function's side effect: every downstream
	// consumer (managed.go's privateNetworkingAzure/
	// privateEndpointRedisAzure) reads these four fields off env
	// exactly as it did before this redesign -- only where the values
	// come from changed.
	app := &models.Application{Name: "myapp"}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	if err := appSubnetsAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil); err != nil {
		t.Fatalf("appSubnetsAzure failed: %v", err)
	}
	if env.InfrastructureSubnetID == "" {
		t.Errorf("expected env.InfrastructureSubnetID to be set")
	}
	if env.PostgresqlSubnetID == nil || *env.PostgresqlSubnetID == "" {
		t.Errorf("expected env.PostgresqlSubnetID to be set")
	}
	if env.MysqlSubnetID == nil || *env.MysqlSubnetID == "" {
		t.Errorf("expected env.MysqlSubnetID to be set")
	}
	if env.RedisSubnetID == nil || *env.RedisSubnetID == "" {
		t.Errorf("expected env.RedisSubnetID to be set")
	}
}

// TestInferAzure_TwoAppsWithDifferentSubnetIndexesDoNotShareSubnets is
// the real integration test: InferAzure itself (not just
// appSubnetsAzure in isolation) produces genuinely isolated per-app
// networking for two different apps sharing one environment, which is
// the entire point of docs/azure-app-isolation-design.md.
func TestInferAzure_TwoAppsWithDifferentSubnetIndexesDoNotShareSubnets(t *testing.T) {
	t.Parallel()
	app1 := &models.Application{Name: "app-one", Services: []models.Service{{Name: "web", Capability: models.CapabilityContainer}}}
	app2 := &models.Application{Name: "app-two", Services: []models.Service{{Name: "web", Capability: models.CapabilityContainer}}}

	env1 := testAppEnv(0)
	resources1, err := InferAzure(app1, &env1)
	if err != nil {
		t.Fatalf("InferAzure(app1) failed: %v", err)
	}

	env2 := testAppEnv(1)
	resources2, err := InferAzure(app2, &env2)
	if err != nil {
		t.Fatalf("InferAzure(app2) failed: %v", err)
	}

	for key := range resources1.Subnet {
		cidr1 := resources1.Subnet[key].AddressPrefixes[0]
		cidr2 := resources2.Subnet[key].AddressPrefixes[0]
		if cidr1 == cidr2 {
			t.Errorf("%s subnet CIDR %s is shared between app-one and app-two -- not actually isolated", key, cidr1)
		}
	}
}
