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
// with `cloudcompose main --env <output>` (the environment directory
// itself, once `terraform apply` has run in it -- cloudcompose main reads
// the resulting facts directly via `terraform output -json`, not a
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
// This is a deliberate simplification (2026-08-09): earlier revisions
// of this command accepted ~14 decision flags that silently overrode
// the file field-by-field when both were given. That merge logic added
// real complexity (a three-way precedence -- flag, file, hardcoded
// default -- tracked via cobra's Changed(), invisible unless you read
// the source) for a command whose entire point is to be the one
// reviewable, authored source of truth for an environment, the same way
// docker-compose.yml is for an app: nobody expects `docker compose`
// itself to take per-field override flags for compose.yml, and there's
// no reason environment.yaml should be different now that a real,
// copyable example exists (examples/hello/environment.yaml).
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a shared infrastructure environment",
	Long: "Initialize a shared infrastructure environment.\n\n" +
		"Creates the VPC, subnets, ALB/Container Apps Environment, and other " +
		"shared resources that multiple applications can use. This is typically " +
		"run once by a platform team, and then developers deploy apps with " +
		"`cloudcompose main`.\n\n" +
		"Reads an authored environment.yaml -- there are no decision flags; " +
		"to change a decision, edit the file. See docs/authored-environment-config.md " +
		"for the schema and examples/hello/environment.yaml for a starting point.",
	Run: runInit,
}

func runInit(cmd *cobra.Command, args []string) {
	configFile, _ := cmd.Flags().GetString("file")
	output, _ := cmd.Flags().GetString("output")

	fileConfig, err := initconfig.Load(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if fileConfig == nil {
		fmt.Fprintf(os.Stderr, "Error: %s not found.\n\n", configFile)
		fmt.Fprintln(os.Stderr, "cloudcompose init reads an authored environment.yaml -- there are no")
		fmt.Fprintln(os.Stderr, "decision flags. Create one (see examples/hello/environment.yaml for")
		fmt.Fprintln(os.Stderr, "a starting point, or docs/authored-environment-config.md for the full")
		fmt.Fprintln(os.Stderr, "schema), then run:")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "  cloudcompose init -f %s\n", configFile)
		os.Exit(1)
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

	if output == "" {
		output = name + "-infrastructure"
	}

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
			fileConfig.Tags, retainData,
		)
	case "azure":
		vpcCIDR := ""
		if fileConfig.Azure != nil {
			vpcCIDR = fileConfig.Azure.VnetCIDR
			fmt.Printf("VNet CIDR: %s\n", vpcCIDR)
		}
		terraformJSON, err = azure.GenerateAzureEnvironment(name, region, vpcCIDR, fileConfig.Tags, retainData)
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
		terraformJSON, err = gcp.GenerateGcpEnvironment(name, region, vpcCIDR, projectID, domain, fileConfig.Tags, retainData)
	default:
		// initconfig.Validate already rejects an unsupported provider
		// before Load ever returns, so this is unreachable in practice --
		// kept as a defensive default rather than a panic.
		fmt.Fprintf(os.Stderr, "Error: provider %q is not supported. Supported: aws, azure, gcp\n", fileConfig.Provider)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(output, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Unexpected error: %v\n", err)
		os.Exit(1)
	}
	tfFile := filepath.Join(output, "main.tf.json")
	if err := os.WriteFile(tfFile, []byte(terraformJSON), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Unexpected error: %v\n", err)
		os.Exit(1)
	}

	// Write a copy of the authored config back out next to main.tf.json
	// (identical to the input, since there are no overrides to resolve
	// anymore) so the file that produced this infrastructure is always
	// sitting next to it, not just implied by shell history. See
	// docs/authored-environment-config.md's "cloudcompose init behavior".
	resolvedYAMLPath := filepath.Join(output, "environment.yaml")
	resolvedYAML, err := yaml.Marshal(fileConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unexpected error: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(resolvedYAMLPath, resolvedYAML, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Unexpected error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Success! Environment initialized.")
	fmt.Println()
	fmt.Println("Generated files:")
	fmt.Printf("  %s - Terraform manifest for shared infrastructure\n", tfFile)
	fmt.Printf("  %s - Copy of the authored config that produced it\n", resolvedYAMLPath)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. cd %s\n", output)
	fmt.Println("  2. terraform init")
	fmt.Println("  3. terraform apply")
	fmt.Println()
	fmt.Println("Deploy an app:")
	fmt.Printf("  cloudcompose main --env %s\n", output)
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

	initCmd.Flags().StringP("file", "f", "environment.yaml", "Path to the authored environment.yaml (see docs/authored-environment-config.md)")
	initCmd.Flags().StringP("output", "o", "", "Output directory for generated files (default: <name>-infrastructure)")
}
