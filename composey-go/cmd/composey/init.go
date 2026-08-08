package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gecburton/composey/internal/compiler/aws"
	"github.com/gecburton/composey/internal/compiler/azure"
	"github.com/gecburton/composey/internal/compiler/gcp"
	"github.com/gecburton/composey/internal/compiler/initconfig"
	"github.com/gecburton/composey/internal/models"
	yaml "go.yaml.in/yaml/v4"

	"github.com/spf13/cobra"
)

// initCmd mirrors cli_env.py's `init` command: bootstraps shared
// platform infrastructure (VPC, ALB/Container Apps Environment, ECS
// Cluster, etc.) that multiple applications deploy to. Typically run
// once by a platform team, then developers deploy apps with `composey
// main --env <output>` (the environment directory itself, once
// `terraform apply` has run in it -- composey main reads the resulting
// facts directly via `terraform output -json`, not a generated file).
//
// Reads an authored `environment.yaml` (the decisions -- region, VPC
// CIDR, whether to create an ALB, a GCP project ID) if one exists in the
// current directory or is given via -f/--file, with any CLI flag
// explicitly passed on the command line overriding the corresponding
// file value for this invocation only. Falls back to flags-only if no
// file is found -- not a breaking change from the previous flags-only
// design. See docs/authored-environment-config.md for the full design
// this implements.
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a shared infrastructure environment",
	Long: "Initialize a shared infrastructure environment.\n\n" +
		"Creates the VPC, subnets, ALB/Container Apps Environment, and other " +
		"shared resources that multiple applications can use. This is typically " +
		"run once by a platform team, and then developers deploy apps with " +
		"`composey main`.\n\n" +
		"Reads an authored environment.yaml if one exists (see " +
		"docs/authored-environment-config.md); CLI flags override its values " +
		"for this invocation. Falls back to flags-only if no file is found.",
	Run: runInit,
}

func runInit(cmd *cobra.Command, args []string) {
	configFile, _ := cmd.Flags().GetString("file")
	provider, _ := cmd.Flags().GetString("provider")
	name, _ := cmd.Flags().GetString("name")
	region, _ := cmd.Flags().GetString("region")
	output, _ := cmd.Flags().GetString("output")
	vpcCIDR, _ := cmd.Flags().GetString("vpc-cidr")
	azCount, _ := cmd.Flags().GetInt("az-count")
	createALB, _ := cmd.Flags().GetBool("create-alb")
	certificateARN, _ := cmd.Flags().GetString("certificate-arn")
	awsEndpoint, _ := cmd.Flags().GetString("aws-endpoint")
	projectID, _ := cmd.Flags().GetString("project-id")
	retainData, _ := cmd.Flags().GetBool("retain-data")
	tagsJSON, _ := cmd.Flags().GetString("tags")

	fileConfig, err := initconfig.Load(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Resolve every decision: an explicitly-passed flag overrides the
	// file; otherwise the file's value is used; otherwise (no file, no
	// flag) the flag's own zero-value/default applies exactly as before
	// this design existed.
	if fileConfig != nil {
		if !cmd.Flags().Changed("provider") && fileConfig.Provider != "" {
			provider = fileConfig.Provider
		}
		if !cmd.Flags().Changed("name") && fileConfig.Name != "" {
			name = fileConfig.Name
		}
		if !cmd.Flags().Changed("region") && fileConfig.Region != "" {
			region = fileConfig.Region
		}
		if !cmd.Flags().Changed("retain-data") && fileConfig.RetainDataOnDestroy != nil {
			retainData = *fileConfig.RetainDataOnDestroy
		}
		if len(tagsJSON) == 0 && len(fileConfig.Tags) > 0 {
			// Tags have no single-flag "changed" check the way scalars
			// do; an empty --tags flag value means "use the file," a
			// non-empty one means "override it entirely," matching how
			// every other field here treats an explicit flag as the
			// stronger signal.
			tags := fileConfig.Tags
			applyFileTags(&tagsJSON, tags)
		}

		if fileConfig.AWS != nil {
			if !cmd.Flags().Changed("vpc-cidr") && fileConfig.AWS.VpcCIDR != "" {
				vpcCIDR = fileConfig.AWS.VpcCIDR
			}
			if !cmd.Flags().Changed("az-count") && fileConfig.AWS.AzCount != nil {
				azCount = *fileConfig.AWS.AzCount
			}
			if !cmd.Flags().Changed("create-alb") && fileConfig.AWS.CreateALB != nil {
				createALB = *fileConfig.AWS.CreateALB
			}
			if !cmd.Flags().Changed("certificate-arn") && fileConfig.AWS.CertificateArn != nil {
				certificateARN = *fileConfig.AWS.CertificateArn
			}
			if !cmd.Flags().Changed("aws-endpoint") && fileConfig.AWS.AwsEndpoint != nil {
				awsEndpoint = *fileConfig.AWS.AwsEndpoint
			}
		}
		if fileConfig.Azure != nil {
			if !cmd.Flags().Changed("vpc-cidr") && fileConfig.Azure.VnetCIDR != "" {
				vpcCIDR = fileConfig.Azure.VnetCIDR
			}
		}
		if fileConfig.Gcp != nil {
			if !cmd.Flags().Changed("vpc-cidr") && fileConfig.Gcp.VpcCIDR != "" {
				vpcCIDR = fileConfig.Gcp.VpcCIDR
			}
			if !cmd.Flags().Changed("project-id") && fileConfig.Gcp.ProjectID != "" {
				projectID = fileConfig.Gcp.ProjectID
			}
		}
	}

	if name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required (or set name: in environment.yaml)")
		os.Exit(1)
	}

	supportedProviders := map[string]bool{"aws": true, "azure": true, "gcp": true}
	providerLower := lowerASCII(provider)
	if !supportedProviders[providerLower] {
		fmt.Fprintf(os.Stderr, "Error: Provider '%s' is not supported. Supported: aws, azure, gcp\n", provider)
		os.Exit(1)
	}

	if !cmd.Flags().Changed("region") && (fileConfig == nil || fileConfig.Region == "") {
		switch providerLower {
		case "aws":
			region = "eu-west-2"
		case "azure":
			region = "eastus"
		case "gcp":
			region = "us-central1"
		}
	}

	if providerLower == "gcp" && projectID == "" {
		fmt.Fprintln(os.Stderr, "Error: --project-id is required for GCP (or set gcp.project_id: in environment.yaml)")
		os.Exit(1)
	}

	if output == "" {
		output = name + "-infrastructure"
	}

	var tags map[string]string
	if tagsJSON != "" {
		var rawTags map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tagsJSON), &rawTags); err != nil {
			fmt.Fprintln(os.Stderr, `Error: Invalid JSON in --tags. Use format: '{"Key": "Value"}'`)
			os.Exit(1)
		}
		tags = map[string]string{}
		for k, v := range rawTags {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				fmt.Fprintln(os.Stderr, `Error: Invalid JSON in --tags. Use format: '{"Key": "Value"}'`)
				os.Exit(1)
			}
			tags[k] = s
		}
	}

	// Assemble the resolved config (file + overrides) so it can be
	// written back to disk, whether or not one existed there already.
	resolvedConfig := buildResolvedConfig(
		providerLower, name, region, tags, retainData,
		vpcCIDR, azCount, createALB, certificateARN, awsEndpoint, projectID,
	)

	fmt.Printf("Initializing %s environment: %s\n", provider, name)
	fmt.Printf("Region: %s\n", region)
	fmt.Printf("Output: %s\n", output)
	fmt.Printf("VPC CIDR: %s\n", vpcCIDR)
	if providerLower == "aws" {
		fmt.Printf("AZ Count: %d\n", azCount)
		fmt.Printf("Create ALB: %t\n", createALB)
	}
	if providerLower == "gcp" {
		fmt.Printf("Project ID: %s\n", projectID)
	}

	var terraformJSON string
	switch providerLower {
	case "aws":
		var certPtr, endpointPtr *string
		if certificateARN != "" {
			certPtr = &certificateARN
		}
		if awsEndpoint != "" {
			endpointPtr = &awsEndpoint
		}
		terraformJSON, err = aws.GenerateAwsEnvironment(
			name, region, vpcCIDR, azCount, createALB, certPtr, endpointPtr,
			tags, retainData,
		)
	case "azure":
		terraformJSON, err = azure.GenerateAzureEnvironment(name, region, vpcCIDR, tags, retainData)
	case "gcp":
		terraformJSON, err = gcp.GenerateGcpEnvironment(name, region, vpcCIDR, projectID, tags, retainData)
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

	// Write the resolved environment.yaml back out (creating it if this
	// was a flags-only invocation, or confirming an existing one is
	// unchanged) so the file that produced this infrastructure is always
	// sitting next to it, not just implied by shell history. See
	// docs/authored-environment-config.md's "composey init behavior".
	resolvedYAMLPath := filepath.Join(output, "environment.yaml")
	resolvedYAML, err := yaml.Marshal(resolvedConfig)
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
	fmt.Printf("  %s - Authored decisions that produced it\n", resolvedYAMLPath)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. cd %s\n", output)
	fmt.Println("  2. terraform init")
	fmt.Println("  3. terraform apply")
	fmt.Println()
	fmt.Println("Deploy an app:")
	fmt.Printf("  composey main --env %s\n", output)
}

// buildResolvedConfig assembles a models.InitConfig from the fully
// resolved (file + flag-override) decisions, for hashing and for writing
// back to disk. Only the block matching provider is populated, matching
// the strict/discriminated schema initconfig.Validate enforces on read.
func buildResolvedConfig(
	provider, name, region string,
	tags map[string]string,
	retainData bool,
	vpcCIDR string,
	azCount int,
	createALB bool,
	certificateARN, awsEndpoint, projectID string,
) *models.InitConfig {
	config := &models.InitConfig{
		Provider:            provider,
		Name:                name,
		Region:              region,
		Tags:                tags,
		RetainDataOnDestroy: &retainData,
	}

	switch provider {
	case "aws":
		aws := &models.AwsInitConfig{VpcCIDR: vpcCIDR, AzCount: &azCount, CreateALB: &createALB}
		if certificateARN != "" {
			aws.CertificateArn = &certificateARN
		}
		if awsEndpoint != "" {
			aws.AwsEndpoint = &awsEndpoint
		}
		config.AWS = aws
	case "azure":
		config.Azure = &models.AzureInitConfig{VnetCIDR: vpcCIDR}
	case "gcp":
		config.Gcp = &models.GcpInitConfig{VpcCIDR: vpcCIDR, ProjectID: projectID}
	}

	return config
}

// applyFileTags is a tiny helper so the tags-from-file branch above
// reads as one line at its call site: JSON-encodes the file's tags map
// back into the same --tags JSON-string representation the rest of this
// function already expects, so both paths (flag, file) converge on one
// piece of parsing code below.
func applyFileTags(tagsJSON *string, tags map[string]string) {
	raw, err := json.Marshal(tags)
	if err != nil {
		return
	}
	*tagsJSON = string(raw)
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

	initCmd.Flags().StringP("file", "f", "environment.yaml", "Path to an authored environment.yaml (decisions); CLI flags override its values")
	initCmd.Flags().StringP("provider", "p", "aws", "Cloud provider (aws, azure, gcp)")
	initCmd.Flags().StringP("name", "n", "", "Environment name (e.g., prod, staging, dev)")
	initCmd.Flags().StringP("region", "r", "", "Cloud region (default: eu-west-2 for AWS, eastus for Azure, us-central1 for GCP)")
	initCmd.Flags().StringP("output", "o", "", "Output directory for generated files")
	initCmd.Flags().String("vpc-cidr", "10.0.0.0/16", "CIDR block for the VPC/VNet")
	initCmd.Flags().Int("az-count", 2, "Number of availability zones (AWS only)")
	initCmd.Flags().Bool("create-alb", true, "Create a shared ALB (AWS only)")
	initCmd.Flags().String("certificate-arn", "", "ACM certificate ARN for HTTPS (AWS only)")
	initCmd.Flags().String("aws-endpoint", "", "Custom endpoint for AWS services (e.g., LocalStack)")
	initCmd.Flags().String("project-id", "", "GCP project ID (GCP only, required)")
	initCmd.Flags().Bool("retain-data", true, "Whether destroying the stack preserves data (snapshots, etc.)")
	initCmd.Flags().String("tags", "", `Tags as JSON object (e.g., '{"Team": "platform"}')`)
}
