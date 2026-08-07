package compiler

import (
	"github.com/gecburton/composey/internal/models"
)

// InferAWS is the main entry point for AWS inference, mirroring
// inference/__init__.py's infer(). It orchestrates the inference of all
// AWS resources needed to deploy the application.
func InferAWS(app *models.Application, env *models.AwsEnvironment) (*models.AWSResources, error) {
	resources := models.NewAWSResources()

	getName := func(resourceName string) string {
		return env.Name + "-" + app.Name + "-" + resourceName
	}
	tags := env.Tags

	// Whether tearing the stack down preserves what it holds.
	discard := !env.RetainDataOnDestroy

	// Step 1: Infer networking (security groups for networks).
	InferNetworking(resources, app, env, getName, tags)

	// Step 2: Calculate listener priorities for public services.
	priorities := CalculateListenerPriorities(app)

	// Step 3: Create service discovery namespace.
	namespace := InferServiceDiscovery(resources, app, env, getName, tags)

	// Step 4: Infer managed services (RDS, ElastiCache, S3).
	managedConnections := InferManagedServices(resources, app, env, getName, tags, discard)

	// Step 5: Infer compute resources (ECS services, tasks, IAM).
	computeConnections := InferComputeResources(resources, app, env, getName, tags, discard, priorities, namespace)

	// Step 6: Infer scheduled tasks (EventBridge).
	if err := InferScheduledTasks(resources, app, env, getName, tags); err != nil {
		return nil, err
	}

	// Step 7: Infer edge resources (CloudFront, WAF).
	InferEdgeResources(resources, app, env, getName, tags)

	// Step 8: Wire up connections and permissions.
	//
	// Python: `connections = {**managed_connections, **compute_connections}`
	// -- compute_connections' entries win on key collision, which cannot
	// actually happen here (a service is exactly one of a managed substitute
	// or a container, never both), so overwrite order is immaterial; kept
	// in the same order as Python's dict merge anyway for clarity.
	connections := map[string]models.Connection{}
	for k, v := range managedConnections {
		connections[k] = v
	}
	for k, v := range computeConnections {
		connections[k] = v
	}
	if err := InferPermissionsAndWiring(resources, app, env, getName, connections); err != nil {
		return nil, err
	}

	return resources, nil
}
