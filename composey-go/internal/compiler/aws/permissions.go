package aws

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/gecburton/composey/internal/models"
)

// InferPermissionsAndWiring wires up connections and grants IAM
// permissions, mirroring _permissions.py's infer_permissions_and_wiring.
//
// For each container service, resolves environment variables that
// reference managed services, updates task definitions, and grants
// necessary IAM permissions.
func InferPermissionsAndWiring(
	resources *models.AWSResources,
	app *models.Application,
	env *models.AwsEnvironment,
	getName func(string) string,
	connections map[string]models.Connection,
) error {
	// Iteration must be deterministic: Python iterates connections in dict
	// insertion order (services in app.services order, since that's how
	// infer()'s managed+compute connections were built), so ResolveValue's
	// tie-breaking between ambiguously-matching services depends on it.
	connectionOrder := connectionOrderFor(app, connections)

	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityContainer {
			continue
		}

		taskDefKey := service.Name + "_td"
		taskDef, ok := resources.EcsTaskDefinition[taskDefKey]
		if !ok {
			continue
		}

		var containerDefs []map[string]any
		if err := json.Unmarshal([]byte(taskDef.ContainerDefinitions), &containerDefs); err != nil {
			return fmt.Errorf("parse container_definitions for %s: %w", service.Name, err)
		}
		container := containerDefs[0]

		execRoleKey := service.Name + "_exec_role"

		// Resolve environment variables and track references.
		rawEnv, _ := container["environment"].([]any)
		environment := make([]any, 0, len(rawEnv))
		referenced := map[string]struct{}{}

		for _, e := range rawEnv {
			entry, _ := e.(map[string]any)
			name, _ := entry["name"].(string)
			value, _ := entry["value"].(string)

			resolved := ResolveValue(value, connections, connectionOrder)
			if resolved.Service != nil {
				referenced[*resolved.Service] = struct{}{}
			}

			if !resolved.Confidential {
				environment = append(environment, map[string]any{"name": name, "value": resolved.Value})
				continue
			}

			// Confidential values go to Secrets Manager.
			storeConfidentialValue(
				resources, service.Name, name, resolved.Value, resolved.Service,
				app.Name, getName, env.Tags, execRoleKey, container,
			)
		}
		container["environment"] = environment

		// Grant permissions based on references.
		referencedNames := make([]string, 0, len(referenced))
		for name := range referenced {
			referencedNames = append(referencedNames, name)
		}
		sort.Strings(referencedNames)

		for _, serverName := range referencedNames {
			server := findServiceByName(app, serverName)
			if server == nil {
				continue
			}

			switch server.Capability {
			case models.CapabilityDatabase:
				grantDatabasePermissions(resources, service.Name, server.Name, getName, env.Tags, execRoleKey, container)
			case models.CapabilityObjectStorage:
				grantS3Permissions(resources, service.Name, server.Name, getName)
			}
		}

		newContainerDefs, err := json.Marshal(containerDefs)
		if err != nil {
			return fmt.Errorf("marshal container_definitions for %s: %w", service.Name, err)
		}
		taskDef.ContainerDefinitions = string(newContainerDefs)
		resources.EcsTaskDefinition[taskDefKey] = taskDef
	}

	return nil
}

// connectionOrderFor returns connection keys in the order Python's own
// dict-merge (`{**managed_connections, **compute_connections}`, itself
// built by iterating app.services in declaration order within each) would
// produce: services in app.Services order, filtered to those with a
// connection.
func connectionOrderFor(app *models.Application, connections map[string]models.Connection) []string {
	order := make([]string, 0, len(connections))
	for i := range app.Services {
		name := app.Services[i].Name
		if _, ok := connections[name]; ok {
			order = append(order, name)
		}
	}
	return order
}

func findServiceByName(app *models.Application, name string) *models.Service {
	for i := range app.Services {
		if app.Services[i].Name == name {
			return &app.Services[i]
		}
	}
	return nil
}

// storeConfidentialValue stores a confidential value in Secrets Manager and
// wires it to the container, mirroring _permissions.py's
// _store_confidential_value.
func storeConfidentialValue(
	resources *models.AWSResources,
	serviceName, varName, value string,
	referencedService *string,
	appName string,
	getName func(string) string,
	tags map[string]string,
	execRoleKey string,
	container map[string]any,
) {
	urlKey := fmt.Sprintf("%s_%s_url", serviceName, toLowerIdentifier(varName))

	// Python's f"...for {referenced_service}" renders the literal string
	// "None" when referenced_service is None (an f-string calls str() on
	// its argument, and str(None) == "None") -- not, say, an empty string
	// or "nothing". Matched here rather than assumed, since resolve_value
	// can legitimately report no service for a bare value that still
	// turned out confidential in theory (never in practice today, but the
	// description has to render something either way).
	refServiceDesc := "None"
	if referencedService != nil {
		refServiceDesc = *referencedService
	}
	desc := fmt.Sprintf("%s for %s, including credentials for %s", varName, serviceName, refServiceDesc)
	resources.SecretsmanagerSecret[urlKey] = models.SecretsManagerSecret{
		Name:        getName(fmt.Sprintf("%s-%s", serviceName, lowerString(varName))),
		Description: &desc,
		Tags:        tags,
	}

	resources.SecretsmanagerSecretVersion[urlKey+"_v1"] = models.SecretsManagerSecretVersion{
		SecretID:     fmt.Sprintf("${aws_secretsmanager_secret.%s.id}", urlKey),
		SecretString: value,
		// Deliberately no ignore_changes - rotated passwords must reach clients.
	}

	secrets, _ := container["secrets"].([]any)
	secrets = append(secrets, map[string]any{
		"name":      varName,
		"valueFrom": fmt.Sprintf("${aws_secretsmanager_secret.%s.arn}", urlKey),
	})
	container["secrets"] = secrets

	// Grant read access.
	policy := marshalJSONString(newIAMPolicyDocument(IAMPolicyStatement{
		Effect:   "Allow",
		Action:   []string{"secretsmanager:GetSecretValue"},
		Resource: []string{fmt.Sprintf("${aws_secretsmanager_secret.%s.arn}", urlKey)},
	}))
	resources.IamRolePolicy[fmt.Sprintf("%s_%s_policy", serviceName, urlKey)] = models.IamRolePolicy{
		Name:   getName(fmt.Sprintf("%s-%s-policy", serviceName, lowerString(varName))),
		Role:   fmt.Sprintf("${aws_iam_role.%s.name}", execRoleKey),
		Policy: policy,
	}
}

// grantDatabasePermissions grants IAM permissions for database access and
// wires credentials, mirroring _permissions.py's _grant_database_permissions.
func grantDatabasePermissions(
	resources *models.AWSResources,
	clientName, serverName string,
	getName func(string) string,
	tags map[string]string,
	execRoleKey string,
	container map[string]any,
) {
	dbSecretKey := serverName + "_db_secret"

	secrets, _ := container["secrets"].([]any)
	secrets = append(secrets,
		map[string]any{
			"name":      "DB_PASSWORD",
			"valueFrom": fmt.Sprintf("${aws_secretsmanager_secret.%s.arn}:password::", dbSecretKey),
		},
		map[string]any{
			"name":      "DB_USERNAME",
			"valueFrom": fmt.Sprintf("${aws_secretsmanager_secret.%s.arn}:username::", dbSecretKey),
		},
	)
	container["secrets"] = secrets

	policy := marshalJSONString(newIAMPolicyDocument(IAMPolicyStatement{
		Effect:   "Allow",
		Action:   []string{"secretsmanager:GetSecretValue"},
		Resource: []string{fmt.Sprintf("${aws_secretsmanager_secret.%s.arn}", dbSecretKey)},
	}))
	resources.IamRolePolicy[fmt.Sprintf("%s_to_%s_rds_secret", clientName, serverName)] = models.IamRolePolicy{
		Name:   getName(fmt.Sprintf("%s-%s-rds-secret", clientName, serverName)),
		Role:   fmt.Sprintf("${aws_iam_role.%s.name}", execRoleKey),
		Policy: policy,
	}
}

// grantS3Permissions grants IAM permissions for S3 bucket access,
// mirroring _permissions.py's _grant_s3_permissions.
func grantS3Permissions(
	resources *models.AWSResources,
	clientName, serverName string,
	getName func(string) string,
) {
	bucketKey := serverName + "_bucket"
	taskRoleKey := clientName + "_task_role"

	policy := marshalJSONString(newIAMPolicyDocument(IAMPolicyStatement{
		Effect: "Allow",
		Action: []string{"s3:*"},
		Resource: []string{
			fmt.Sprintf("${aws_s3_bucket.%s.arn}", bucketKey),
			fmt.Sprintf("${aws_s3_bucket.%s.arn}/*", bucketKey),
		},
	}))
	resources.IamRolePolicy[fmt.Sprintf("%s_to_%s_s3_policy", clientName, serverName)] = models.IamRolePolicy{
		Name:   getName(fmt.Sprintf("%s-%s-s3-policy", clientName, serverName)),
		Role:   fmt.Sprintf("${aws_iam_role.%s.name}", taskRoleKey),
		Policy: policy,
	}
}

func lowerString(s string) string {
	return toLower(s)
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// toLowerIdentifier mirrors _permissions.py's
// safe_terraform_identifier(var_name).lower(): non-alphanumeric characters
// become underscores, trimmed at both ends, then lowercased.
func toLowerIdentifier(varName string) string {
	return toLower(SafeTerraformIdentifier(varName))
}
