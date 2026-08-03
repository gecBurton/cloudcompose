package compiler

import "testing"

func TestInferCapability(t *testing.T) {
	testCases := []struct {
		image    string
		expected string
	}{
		{"postgres:16", "database"},
		{"postgres", "database"},
		{"pgvector/pgvector:pg17", "database"},
		{"postgis/postgis:16-3.4", "database"},
		{"timescale/timescaledb:latest-pg16", "database"},
		{"bitnami/postgresql:16", "database"},
		{"public.ecr.aws/docker/library/postgres:16", "database"},
		{"mysql:8", "database"},
		{"mariadb:10-focal", "database"},
		{"percona:8.0", "database"},
		{"redis:7", "cache"},
		{"redislabs/redismod", "cache"},
		{"valkey/valkey:8", "cache"},
		{"keydb:latest", "cache"},
		{"minio/minio", "object-storage"},
		{"nginx:latest", "container"},
		{"flask-redis-web:latest", "container"},
	}

	for _, tc := range testCases {
		result := InferCapability(tc.image)
		if result != tc.expected {
			t.Errorf("InferCapability(%s) = %s, want %s", tc.image, result, tc.expected)
		}
	}
}
