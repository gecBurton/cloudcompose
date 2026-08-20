package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
	"github.com/gecburton/cloudcompose/internal/compiler/gcp"
	"github.com/gecburton/cloudcompose/internal/compiler/initconfig"
	yaml "go.yaml.in/yaml/v4"

	"github.com/spf13/cobra"
)

// initCmd bootstraps shared platform infrastructure (VPC, ALB/Container
// Apps Environment, ECS Cluster, etc.) that multiple applications deploy
// to. Typically run once by a platform team, then developers deploy apps
// with `cloudcompose compile --env <output>` (the environment directory
// itself, once `terraform apply` has run in it -- cloudcompose compile
// reads the resulting facts directly via `terraform output -json`, not a
// generated file).
//
// Reads an authored `environment.yaml` -- the decisions (region, VPC
// CIDR, whether to create an ALB, a GCP project ID) -- and nothing
// else: there are no decision flags on this command at all. To change
// a decision, edit the file and re-run `init`; there is exactly one way
// to configure an environment, not two ways whose precedence has to be
// remembered. See docs/authored-environment-config.md for the full
// design and examples/README.md for a real, runnable walkthrough.
//
// This is a deliberate simplification: no decision flags means no
// three-way precedence (flag, file, hardcoded default) to keep
// straight, for a command whose entire point is to be the one
// reviewable, authored source of truth for an environment, the same way
// docker-compose.yml is for an app: nobody expects `docker compose`
// itself to take per-field override flags for compose.yml, and there's
// no reason environment.yaml should be different now that a real,
// copyable example exists (examples/hello/environment.yaml).
//
// Uses -e/--env for this file, not -f/--file: init is the one command
// with no compose file at all, so -f/--file's inherited persistent
// meaning (see main.go's own doc comment) is never relevant here, and
// giving environment.yaml the same -e/--env name `up` uses for its own
// identical input (see up.go's own doc comment on that) keeps "the
// environment" meaning one consistent flag across every command that
// has one -- a file pre-apply (init, up), or a directory post-apply
// (compile/ps/logs/down) -- rather than -f overloaded to mean two
// unrelated kinds of file depending which command it's given to.
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a shared infrastructure environment",
	Long: "Initialize a shared infrastructure environment.\n\n" +
		"Creates the VPC, subnets, ALB/Container Apps Environment, and other " +
		"shared resources that multiple applications can use. This is typically " +
		"run once by a platform team, and then developers deploy apps with " +
		"`cloudcompose compile`.\n\n" +
		"Reads an authored environment.yaml -- there are no decision flags; " +
		"to change a decision, edit the file. See docs/authored-environment-config.md " +
		"for the schema and examples/hello/environment.yaml for a starting point.",
	Run: runInit,
}

func runInit(cmd *cobra.Command, args []string) {
	configFile, _ := cmd.Flags().GetString("env")

	output, err := initEnvironment(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. cd %s\n", output)
	fmt.Println("  2. terraform init")
	fmt.Println("  3. terraform apply")
	fmt.Println()
	fmt.Println("Deploy an app:")
	fmt.Printf("  cloudcompose compile --env %s\n", output)
}

// initEnvironment does everything `cloudcompose init` does -- load
// configFile, generate the environment's Terraform JSON, and write it
// (plus a copy of the resolved config) to <dir of configFile>/env-<name>
// -- and returns that output directory. Extracted from runInit so
// `cloudcompose up` (see up.go) can call the exact same logic directly,
// rather than duplicating it or shelling out to itself.
func initEnvironment(configFile string) (string, error) {
	fileConfig, err := initconfig.Load(configFile)
	if err != nil {
		return "", err
	}
	if fileConfig == nil {
		return "", fmt.Errorf(
			"%s not found.\n\ncloudcompose init reads an authored environment.yaml -- there are no\n"+
				"decision flags. Create one (see examples/hello/environment.yaml for\n"+
				"a starting point, or docs/authored-environment-config.md for the full\n"+
				"schema), then run:\n\n  cloudcompose init -e %s",
			configFile, configFile,
		)
	}

	// Printed here, immediately after a successful load, rather than
	// after generation succeeds below: a human deciding whether to
	// proceed should see these before anything is written to disk, not
	// buried after "Success!" -- and every path through this function
	// (any of the three clouds below) shares this one print site rather
	// than each needing its own. See
	// initconfig.BackendWarnings' own doc comment and
	// docs/multi-user-state.md for what these warn about and why they
	// are warnings, not errors that would block init entirely.
	for _, warning := range initconfig.BackendWarnings(fileConfig) {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
	}

	providerLower := lowerASCII(fileConfig.Provider)
	name := fileConfig.Name
	region := fileConfig.Region
	retainData := true
	if fileConfig.RetainDataOnDestroy != nil {
		retainData = *fileConfig.RetainDataOnDestroy
	}
	var domain string
	if fileConfig.Domain != nil {
		domain = *fileConfig.Domain
	}
	highAvailability := false
	if fileConfig.HighAvailabilityEnabled != nil {
		highAvailability = *fileConfig.HighAvailabilityEnabled
	}
	backupRetentionDays := 7
	if fileConfig.BackupRetentionDays != nil {
		backupRetentionDays = *fileConfig.BackupRetentionDays
	}
	logRetentionDays := 7
	if fileConfig.LogRetentionDays != nil {
		logRetentionDays = *fileConfig.LogRetentionDays
	}

	absConfigFile, err := filepath.Abs(configFile)
	if err != nil {
		return "", err
	}
	output := filepath.Join(filepath.Dir(absConfigFile), "env-"+name)

	fmt.Printf("Initializing %s environment: %s\n", fileConfig.Provider, name)
	fmt.Printf("Region: %s\n", region)
	fmt.Printf("Output: %s\n", output)

	var terraformJSON string
	switch providerLower {
	case "aws":
		awsCfg := fileConfig.AWS
		var certPtr, endpointPtr *string
		azCount := 2
		createALB := true
		if awsCfg != nil {
			if awsCfg.AzCount != nil {
				azCount = *awsCfg.AzCount
			}
			if awsCfg.CreateALB != nil {
				createALB = *awsCfg.CreateALB
			}
			certPtr = awsCfg.CertificateArn
			endpointPtr = awsCfg.AwsEndpoint
			fmt.Printf("VPC CIDR: %s\n", awsCfg.VpcCIDR)
			fmt.Printf("AZ Count: %d\n", azCount)
			fmt.Printf("Create ALB: %t\n", createALB)
		}
		vpcCIDR := ""
		if awsCfg != nil {
			vpcCIDR = awsCfg.VpcCIDR
		}
		terraformJSON, err = aws.GenerateAwsEnvironment(
			name, region, vpcCIDR, azCount, createALB, certPtr, endpointPtr,
			fileConfig.Tags, retainData, highAvailability, backupRetentionDays, logRetentionDays,
			fileConfig.Backend,
		)
	case "azure":
		vpcCIDR := ""
		if fileConfig.Azure != nil {
			vpcCIDR = fileConfig.Azure.VnetCIDR
			fmt.Printf("VNet CIDR: %s\n", vpcCIDR)
		}
		terraformJSON, err = azure.GenerateAzureEnvironment(
			name, region, vpcCIDR, fileConfig.Tags, retainData, highAvailability, backupRetentionDays, logRetentionDays,
			fileConfig.Backend,
		)
	case "gcp":
		vpcCIDR, projectID := "", ""
		if fileConfig.Gcp != nil {
			vpcCIDR = fileConfig.Gcp.VpcCIDR
			projectID = fileConfig.Gcp.ProjectID
			fmt.Printf("VPC CIDR: %s\n", vpcCIDR)
			fmt.Printf("Project ID: %s\n", projectID)
		}
		if domain != "" {
			fmt.Printf("Domain: %s\n", domain)
		}
		terraformJSON, err = gcp.GenerateGcpEnvironment(name, region, vpcCIDR, projectID, domain, fileConfig.Tags, retainData, fileConfig.Backend)
	default:
		// initconfig.Validate already rejects an unsupported provider
		// before Load ever returns, so this is unreachable in practice --
		// kept as a defensive default rather than a panic.
		return "", fmt.Errorf("provider %q is not supported. Supported: aws, azure, gcp", fileConfig.Provider)
	}
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(output, 0755); err != nil {
		return "", err
	}
	tfFile := filepath.Join(output, "main.tf.json")
	if err := os.WriteFile(tfFile, []byte(terraformJSON), 0644); err != nil {
		return "", err
	}

	// Write a copy of the authored config back out next to main.tf.json
	// (identical to the input, since there are no overrides to resolve
	// anymore) so the file that produced this infrastructure is always
	// sitting next to it, not just implied by shell history. See
	// docs/authored-environment-config.md's "cloudcompose init behavior".
	resolvedYAMLPath := filepath.Join(output, "environment.yaml")
	resolvedYAML, err := yaml.Marshal(fileConfig)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(resolvedYAMLPath, resolvedYAML, 0644); err != nil {
		return "", err
	}

	fmt.Println("Success! Environment initialized.")
	fmt.Println()
	fmt.Println("Generated files:")
	fmt.Printf("  %s - Terraform manifest for shared infrastructure\n", tfFile)
	fmt.Printf("  %s - Copy of the authored config that produced it\n", resolvedYAMLPath)

	return output, nil
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().StringP("env", "e", "environment.yaml", "Path to the authored environment.yaml (see docs/authored-environment-config.md). Unlike --env on compile/ps/logs/down (an already-applied environment directory), this is the input file init itself applies.")
}
