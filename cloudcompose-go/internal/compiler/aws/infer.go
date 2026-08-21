package aws

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// InferAWS is the main entry point for AWS inference. It orchestrates the
// inference of all AWS resources needed to deploy the application.
func InferAWS(app *models.Application, env *models.AwsEnvironment) (*models.AWSResources, error) {
	resources := models.NewAWSResources()

	getName := shared.ResourceNamer(env.Name, app.Name)
	tags := env.Tags

	// Whether tearing the stack down preserves what it holds.
	discard := !env.RetainDataOnDestroy

	InferNetworking(resources, app, env, getName, tags)

	priorities := CalculateListenerPriorities(app)

	namespace := InferServiceDiscovery(resources, app, env, getName, tags)

	managedConnections := InferManagedServices(resources, app, env, getName, tags, discard)

	computeConnections := InferComputeResources(resources, app, env, getName, tags, discard, priorities, namespace)

	if err := InferScheduledTasks(resources, app, env, getName, tags); err != nil {
		return nil, err
	}

	InferEdgeResources(resources, app, env, getName, tags)

	// compute_connections' entries win on key collision, which cannot
	// actually happen here (a service is exactly one of a managed substitute
	// or a container, never both).
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
