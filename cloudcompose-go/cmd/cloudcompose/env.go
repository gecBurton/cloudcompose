package main

import (
	"github.com/spf13/cobra"
)

// envCmd is the parent for every subcommand that operates on a shared
// infrastructure environment (VPC, ALB/Container Apps Environment, ECS
// Cluster, etc.) rather than a single app -- `env init`/`env up`/`env
// down`, mirroring `docker compose`'s own noun-then-verb shape.
// `compile` stays top-level, not nested here: see compile.go.
var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage a shared infrastructure environment (VPC, cluster, ALB, ...)",
	Long: "Manage a shared infrastructure environment: the VPC, subnets, " +
		"ALB/Container Apps Environment, and other resources that multiple " +
		"apps can deploy into.\n\n" +
		"`env init` writes the environment's Terraform manifest without " +
		"applying it. `env up` does the same and then runs `terraform apply` " +
		"immediately, for the common case of one app, one environment. " +
		"`env down` runs `terraform destroy` against it, refusing by default " +
		"if any app still depends on it.",
}

func init() {
	rootCmd.AddCommand(envCmd)
}
