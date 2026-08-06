package compiler

import (
	"strings"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

// --- test_networks.py: segment sorting, cap, external rejection ------------

func TestNetworkSegmentsAreSortedForDeterminism(t *testing.T) {
	t.Parallel()
	service := &models.ComposeService{
		Networks: map[string]interface{}{"zebra": nil, "alpha": nil},
	}
	segments, err := NetworkSegmentsFor("web", service, 0)
	if err != nil {
		t.Fatalf("NetworkSegmentsFor failed: %v", err)
	}
	if len(segments) != 2 || segments[0] != "alpha" || segments[1] != "zebra" {
		t.Errorf("segments = %v, want [alpha zebra]", segments)
	}
}

func TestTooManyNetworkSegmentsIsRejected(t *testing.T) {
	t.Parallel()
	networks := map[string]interface{}{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		networks[n] = nil
	}
	service := &models.ComposeService{Networks: networks}
	_, err := NetworkSegmentsFor("web", service, 0)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "joins 6 network segments") {
		t.Errorf("error = %q, want it to mention 'joins 6 network segments'", err.Error())
	}
}

func TestExternalNetworksAreRejected(t *testing.T) {
	t.Parallel()
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {Image: "nginx"},
		},
		Networks: map[string]*models.NetworkDefinition{
			"shared": {External: true},
		},
	}
	_, err := Normalize(app, "p")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "declared external") {
		t.Errorf("error = %q, want it to mention 'declared external'", err.Error())
	}
}
