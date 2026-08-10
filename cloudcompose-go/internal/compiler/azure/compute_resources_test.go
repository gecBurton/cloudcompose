package azure

import (
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// Tests for docs/azure-aws-parity-todo.md's Priority 4 "New gap found"
// item: Azure Container Apps' Consumption plan requires CPU and memory
// to be an exact matched pair from a fixed table, not just independently
// under the 2vCPU/4GiB cap.

func TestAzureCPUMemoryPairAzure_RejectsMismatchedPair(t *testing.T) {
	t.Parallel()
	// 1.0 vCPU only pairs validly with 2.0Gi -- this is exactly the
	// compute-tuning example's worker service (size: medium = 1.0 vCPU,
	// with an explicit memory: 4096 override = 4Gi), which is why that
	// example has no Azure golden fixture: this is the correct behavior,
	// not a bug to fix in the fixture.
	err := azureCPUMemoryPairAzure("worker", 1.0, 4.0)
	if err == nil {
		t.Fatalf("expected an error for 1.0 vCPU + 4Gi (not a valid Consumption pair)")
	}
	if !strings.Contains(err.Error(), "worker") {
		t.Errorf("error should name the service, got: %v", err)
	}
}

func TestAzureCPUMemoryPairAzure_RejectsOffStepCPU(t *testing.T) {
	t.Parallel()
	// Consumption CPU must land on a 0.25 vCPU step; 0.3 doesn't.
	err := azureCPUMemoryPairAzure("web", 0.3, 0.6)
	if err == nil {
		t.Fatalf("expected an error for CPU not on a 0.25 vCPU step")
	}
}

func TestAzureCPUMemoryPairAzure_AllowsEveryDocumentedPair(t *testing.T) {
	t.Parallel()
	// Every pair Microsoft's own vCPU/memory allocation table lists,
	// confirmed against learn.microsoft.com/azure/container-apps/containers
	// (not guessed): 0.25 vCPU steps from 0.25 up to 4.0, each paired
	// with exactly 2x that many GiB. Only the pairs within this
	// project's own 2vCPU/4GiB Consumption-only ceiling
	// (azureConsumptionMaxCPU/azureConsumptionMaxMemoryGB) are relevant
	// here, since getCPUCoresAzure/getMemoryGBAzure already reject
	// anything above that ceiling before azureCPUMemoryPairAzure ever
	// sees it -- but the pairing check itself has no opinion on the
	// ceiling, so this covers the full documented table for
	// completeness.
	pairs := []struct {
		cpu, memoryGB float64
	}{
		{0.25, 0.5},
		{0.5, 1.0},
		{0.75, 1.5},
		{1.0, 2.0},
		{1.25, 2.5},
		{1.5, 3.0},
		{1.75, 3.5},
		{2.0, 4.0},
	}
	for _, p := range pairs {
		if err := azureCPUMemoryPairAzure("web", p.cpu, p.memoryGB); err != nil {
			t.Errorf("expected %g vCPU + %gGi to be a valid pair, got error: %v", p.cpu, p.memoryGB, err)
		}
	}
}

func TestResolveContainerResourcesAzure_RejectsMismatchedCpuMemoryPair(t *testing.T) {
	t.Parallel()
	// The real, end-to-end shape of the bug: a size default (medium =
	// 1.0 vCPU) combined with an independent memory: override. Neither
	// getCPUCoresAzure nor getMemoryGBAzure alone can catch this --
	// each only checks its own value against the 2vCPU/4GiB ceiling,
	// not against the other's resolved value -- which is exactly why
	// resolveContainerResourcesAzure exists as the one place that
	// validates the pair together.
	mem := 4096
	service := &models.Service{Name: "worker", Size: models.ServiceSizeMedium, Memory: &mem}
	_, _, err := resolveContainerResourcesAzure(service)
	if err == nil {
		t.Fatalf("expected an error for size: medium (1.0 vCPU) + memory: 4096 (4Gi), not a valid Consumption pair")
	}
}

func TestResolveContainerResourcesAzure_AllowsMatchedExplicitOverrides(t *testing.T) {
	t.Parallel()
	// The compute-tuning example's other service (api): explicit
	// cpu: 1024 (1.0 vCPU) + memory: 2048 (2Gi) -- a valid pair set
	// entirely independently of size:, which should still work.
	cpu := 1024
	mem := 2048
	service := &models.Service{Name: "api", CPU: &cpu, Memory: &mem}
	gotCPU, gotMemory, err := resolveContainerResourcesAzure(service)
	if err != nil {
		t.Fatalf("resolveContainerResourcesAzure failed: %v", err)
	}
	if gotCPU != 1.0 {
		t.Errorf("cpu = %v, want 1.0", gotCPU)
	}
	if gotMemory != "2048Mi" {
		t.Errorf("memory = %v, want 2048Mi", gotMemory)
	}
}

func TestResolveContainerResourcesAzure_AllowsSizeDefaultsAlone(t *testing.T) {
	t.Parallel()
	// Every plain size: default must itself be a valid pair, since
	// shared.SizeMappings' three sizes all happen to satisfy Memory ==
	// 2x CPU (512=2x256, 2048=2x1024, 8192=2x4096) -- this is a
	// regression test for that property holding, not just a smoke test.
	for _, size := range []models.ServiceSize{models.ServiceSizeSmall, models.ServiceSizeMedium} {
		service := &models.Service{Name: "web", Size: size}
		if _, _, err := resolveContainerResourcesAzure(service); err != nil {
			t.Errorf("size %q should resolve to a valid pair, got error: %v", size, err)
		}
	}
}

func TestMemoryGBFromContainerAppsString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want float64
	}{
		{"512Mi", 0.5},
		{"2048Mi", 2.0},
		{"4Gi", 4.0},
		{"0.5Gi", 0.5},
	}
	for _, c := range cases {
		got, err := memoryGBFromContainerAppsString(c.in)
		if err != nil {
			t.Errorf("memoryGBFromContainerAppsString(%q) failed: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("memoryGBFromContainerAppsString(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMemoryGBFromContainerAppsString_RejectsUnknownSuffix(t *testing.T) {
	t.Parallel()
	if _, err := memoryGBFromContainerAppsString("2048"); err == nil {
		t.Fatalf("expected an error for a memory string with no Mi/Gi suffix")
	}
}
