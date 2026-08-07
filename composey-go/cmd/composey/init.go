package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/gecburton/composey/internal/compiler"
	"github.com/spf13/cobra"
)

// initCmd mirrors cli_env.py's `init` command: bootstraps shared
// platform infrastructure (VPC, ALB/Container Apps Environment, ECS
// Cluster, etc.) that multiple applications deploy to. Typically run
// once by a platform team, then developers deploy apps with `composey
// main --env <output>/environment.yml`.
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a shared infrastructure environment",
	Long: "Initialize a shared infrastructure environment.\n\n" +
		"Creates the VPC, subnets, ALB/Container Apps Environment, and other " +
		"shared resources that multiple applications can use. This is typically " +
		"run once by a platform team, and then developers deploy apps with " +
		"`composey main`.",
	Run: runInit,
}

func runInit(cmd *cobra.Command, args []string) {
	provider, _ := cmd.Flags().GetString("provider")
	name, _ := cmd.Flags().GetString("name")
	region, _ := cmd.Flags().GetString("region")
	output, _ := cmd.Flags().GetString("output")
	vpcCIDR, _ := cmd.Flags().GetString("vpc-cidr")
	azCount, _ := cmd.Flags().GetInt("az-count")
	createALB, _ := cmd.Flags().GetBool("create-alb")
	certificateARN, _ := cmd.Flags().GetString("certificate-arn")
	awsEndpoint, _ := cmd.Flags().GetString("aws-endpoint")
	retainData, _ := cmd.Flags().GetBool("retain-data")
	tagsJSON, _ := cmd.Flags().GetString("tags")

	if name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		os.Exit(1)
	}

	supportedProviders := map[string]bool{"aws": true, "azure": true, "gcp": true}
	providerLower := lowerASCII(provider)
	if !supportedProviders[providerLower] {
		fmt.Fprintf(os.Stderr, "Error: Provider '%s' is not supported. Supported: aws, azure, gcp\n", provider)
		os.Exit(1)
	}

	if !cmd.Flags().Changed("region") {
		switch providerLower {
		case "aws":
			region = "eu-west-2"
		case "azure":
			region = "eastus"
		case "gcp":
			region = "us-central1"
		}
	}

	if output == "" {
		output = name + "-infrastructure"
	}

	var tags map[string]string
	var tagOrder []string
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
			tagOrder = append(tagOrder, k)
		}
		sort.Strings(tagOrder)
	}

	fmt.Printf("Initializing %s environment: %s\n", provider, name)
	fmt.Printf("Region: %s\n", region)
	fmt.Printf("Output: %s\n", output)
	fmt.Printf("VPC CIDR: %s\n", vpcCIDR)
	if providerLower == "aws" {
		fmt.Printf("AZ Count: %d\n", azCount)
		fmt.Printf("Create ALB: %t\n", createALB)
	}

	var terraformJSON string
	var err error
	switch providerLower {
	case "aws":
		var certPtr, endpointPtr *string
		if certificateARN != "" {
			certPtr = &certificateARN
		}
		if awsEndpoint != "" {
			endpointPtr = &awsEndpoint
		}
		terraformJSON, err = compiler.GenerateAwsEnvironment(
			name, region, vpcCIDR, azCount, createALB, certPtr, endpointPtr,
			tags, tagOrder, retainData,
		)
	case "azure":
		terraformJSON, err = compiler.GenerateAzureEnvironment(name, region, vpcCIDR, tags, tagOrder, retainData)
	case "gcp":
		terraformJSON, err = compiler.GenerateGcpEnvironment(name, region, vpcCIDR, tags, tagOrder, retainData)
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

	fmt.Println("Success! Environment initialized.")
	fmt.Println()
	fmt.Println("Generated files:")
	fmt.Printf("  %s - Terraform manifest for shared infrastructure\n", tfFile)
	fmt.Println("  environment.yml - Will be created by Terraform apply")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. cd %s\n", output)
	fmt.Println("  2. terraform init")
	fmt.Println("  3. terraform apply")
	fmt.Println()
	fmt.Println("Deploy an app:")
	fmt.Printf("  composey main --env %s/environment.yml\n", output)
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

	initCmd.Flags().StringP("provider", "p", "aws", "Cloud provider (aws, azure, gcp)")
	initCmd.Flags().StringP("name", "n", "", "Environment name (e.g., prod, staging, dev)")
	initCmd.Flags().StringP("region", "r", "", "Cloud region (default: eu-west-2 for AWS, eastus for Azure, us-central1 for GCP)")
	initCmd.Flags().StringP("output", "o", "", "Output directory for generated files")
	initCmd.Flags().String("vpc-cidr", "10.0.0.0/16", "CIDR block for the VPC/VNet")
	initCmd.Flags().Int("az-count", 2, "Number of availability zones (AWS only)")
	initCmd.Flags().Bool("create-alb", true, "Create a shared ALB (AWS only)")
	initCmd.Flags().String("certificate-arn", "", "ACM certificate ARN for HTTPS (AWS only)")
	initCmd.Flags().String("aws-endpoint", "", "Custom endpoint for AWS services (e.g., LocalStack)")
	initCmd.Flags().String("azure-endpoint", "", "Custom endpoint for Azure services")
	initCmd.Flags().String("gcp-endpoint", "", "Custom endpoint for GCP services")
	initCmd.Flags().Bool("retain-data", true, "Whether destroying the stack preserves data (snapshots, etc.)")
	initCmd.Flags().String("tags", "", `Tags as JSON object (e.g., '{"Team": "platform"}')`)
}
