package main

import (
	"fmt"
	"os"
	"os/exec"
)

// terraformApply runs `terraform init` then `terraform apply` in dir,
// with stdin/stdout/stderr connected to the terminal. If autoApprove is
// false, stdin is connected and no -auto-approve is passed, so
// Terraform's own confirmation prompt behaves normally. Shared by `env
// up` and `compose up`.
func terraformApply(dir string, autoApprove bool) error {
	fmt.Printf("Running terraform in %s\n", dir)

	if err := terraformInit(dir); err != nil {
		return err
	}

	args := []string{"apply"}
	if autoApprove {
		args = append(args, "-auto-approve")
	}
	applyCmd := exec.Command("terraform", args...)
	applyCmd.Dir = dir
	if !autoApprove {
		applyCmd.Stdin = os.Stdin
	}
	applyCmd.Stdout = os.Stdout
	applyCmd.Stderr = os.Stderr
	if err := applyCmd.Run(); err != nil {
		return fmt.Errorf("terraform apply in %s: %w", dir, err)
	}
	return nil
}

// terraformInit runs `terraform init` in dir.
func terraformInit(dir string) error {
	initCmd := exec.Command("terraform", "init", "-input=false")
	initCmd.Dir = dir
	initCmd.Stdout = os.Stdout
	initCmd.Stderr = os.Stderr
	if err := initCmd.Run(); err != nil {
		return fmt.Errorf("terraform init in %s: %w", dir, err)
	}
	return nil
}

// terraformDestroy runs `terraform init` and then `terraform destroy`
// in dir. Interactive with stdin connected and no -auto-approve when
// autoApprove is false, or -auto-approve passed with stdin left
// unconnected when true. Shared by `env down` and `compose down`.
func terraformDestroy(dir string, autoApprove bool) error {
	fmt.Printf("Running terraform destroy in %s\n", dir)

	if err := terraformInit(dir); err != nil {
		return err
	}

	args := []string{"destroy"}
	if autoApprove {
		args = append(args, "-auto-approve")
	}
	destroyCmd := exec.Command("terraform", args...)
	destroyCmd.Dir = dir
	if !autoApprove {
		destroyCmd.Stdin = os.Stdin
	}
	destroyCmd.Stdout = os.Stdout
	destroyCmd.Stderr = os.Stderr
	if err := destroyCmd.Run(); err != nil {
		return fmt.Errorf("terraform destroy in %s: %w", dir, err)
	}
	return nil
}
