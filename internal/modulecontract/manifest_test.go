package modulecontract

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolveOptionalIntegration(t *testing.T) {
	api := validManifest("go-api", "application.http-api")
	api.Optional = []Dependency{{Capability: "deployment.docker-compose", Version: ">=1.0.0"}}
	local := validManifest("docker-compose", "deployment.docker-compose")
	got, err := Resolve([]Manifest{local, api})
	if err != nil {
		t.Fatal(err)
	}
	if got.Modules[0] != "docker-compose" || got.Modules[1] != "go-api" {
		t.Fatalf("modules are not sorted: %#v", got.Modules)
	}
	if got.Connections["go-api"][0] != "docker-compose:deployment.docker-compose" {
		t.Fatalf("unexpected connection: %#v", got.Connections)
	}
	if len(got.Unconnected) != 0 {
		t.Fatalf("unexpected unresolved optionals: %#v", got.Unconnected)
	}
	var first, second bytes.Buffer
	if err := got.Encode(&first); err != nil {
		t.Fatal(err)
	}
	if err := got.Encode(&second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("encoding is not deterministic")
	}
}

func TestResolveOptionalIntegrationAbsentIsAllowed(t *testing.T) {
	api := validManifest("go-api", "application.http-api")
	api.Optional = []Dependency{{Capability: "deployment.docker-compose"}}
	got, err := Resolve([]Manifest{api})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Unconnected) != 1 || got.Unconnected[0] != "go-api:deployment.docker-compose" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestResolveRejectsMissingRequiredCapability(t *testing.T) {
	api := validManifest("python-api", "application.http-api")
	api.Requires = []Dependency{{Capability: "data.postgresql"}}
	_, err := Resolve([]Manifest{api})
	if err == nil || !strings.Contains(err.Error(), `requires capability "data.postgresql"`) {
		t.Fatalf("expected clear compatibility error, got %v", err)
	}
}

func TestResolveRejectsDuplicateAndInvalidManifest(t *testing.T) {
	valid := validManifest("go-api", "application.http-api")
	if _, err := Resolve([]Manifest{valid, valid}); err == nil {
		t.Fatal("expected duplicate module error")
	}
	valid.Version = "latest"
	if err := Validate(valid); err == nil {
		t.Fatal("expected invalid version error")
	}
}

func TestResolveHonorsProviderVersionConstraint(t *testing.T) {
	consumer := validManifest("go-api", "application.http-api")
	consumer.Requires = []Dependency{{Capability: "storage.object", Version: ">=2.0.0"}}
	oldProvider := validManifest("minio", "storage.object")
	oldProvider.Version = "1.9.9"
	if _, err := Resolve([]Manifest{consumer, oldProvider}); err == nil || !strings.Contains(err.Error(), `version ">=2.0.0"`) {
		t.Fatalf("expected incompatible-version error, got %v", err)
	}
	oldProvider.Version = "2.1.0"
	if _, err := Resolve([]Manifest{consumer, oldProvider}); err != nil {
		t.Fatalf("expected compatible provider: %v", err)
	}
}

func TestValidateRequiresOwnershipAndCleanupContract(t *testing.T) {
	m := validManifest("go-api", "application.http-api")
	m.Cleanup = "delete-everything"
	if err := Validate(m); err == nil || !strings.Contains(err.Error(), "cleanup_behavior") {
		t.Fatalf("expected cleanup behavior validation, got %v", err)
	}
}

func validManifest(name, capability string) Manifest {
	return Manifest{Name: name, Version: "1.0.0", ContractVersion: "1.0.0", Category: "backend", Provides: []string{capability}, Ownership: Ownership{Scope: "environment"}, Cleanup: "environment-owned-only"}
}
