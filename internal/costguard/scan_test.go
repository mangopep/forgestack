package costguard

import (
	"os"
	"testing"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name) //nolint:gosec // G304: name is a fixed literal from each test case, not external input
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return data
}

func TestScanPlanCheapHasNoCostlyResources(t *testing.T) {
	resources, err := ScanPlan(readTestdata(t, "plan-cheap.json"))
	if err != nil {
		t.Fatalf("ScanPlan: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d: %+v", len(resources), resources)
	}
	flags := FlagCostlyResources(resources, Budget{})
	if len(flags) != 0 {
		t.Errorf("expected no costly-resource flags for an S3 bucket, got %+v", flags)
	}
}

func TestScanPlanFlagsNATGateway(t *testing.T) {
	resources, err := ScanPlan(readTestdata(t, "plan-nat-gateway.json"))
	if err != nil {
		t.Fatalf("ScanPlan: %v", err)
	}
	flags := FlagCostlyResources(resources, Budget{})
	if len(flags) != 1 || flags[0].ResourceType != "aws_nat_gateway" {
		t.Fatalf("expected exactly one aws_nat_gateway flag, got %+v", flags)
	}
	if flags[0].ExplicitlyApproved {
		t.Error("expected ExplicitlyApproved to be false with no approved_costly_resource_types set")
	}
}

func TestScanPlanFlagsEKSClusterAndNodeGroup(t *testing.T) {
	resources, err := ScanPlan(readTestdata(t, "plan-eks.json"))
	if err != nil {
		t.Fatalf("ScanPlan: %v", err)
	}
	flags := FlagCostlyResources(resources, Budget{})
	if len(flags) != 2 {
		t.Fatalf("expected both the cluster and the node group flagged, got %+v", flags)
	}
}

func TestScanPlanFlagsLargeInstanceType(t *testing.T) {
	resources, err := ScanPlan(readTestdata(t, "plan-large-instance.json"))
	if err != nil {
		t.Fatalf("ScanPlan: %v", err)
	}
	if resources[0].InstanceType != "m5.8xlarge" {
		t.Fatalf("expected instance type to be extracted, got %+v", resources[0])
	}
	flags := FlagCostlyResources(resources, Budget{})
	if len(flags) != 1 {
		t.Fatalf("expected the large instance type to be flagged, got %+v", flags)
	}
}

func TestScanPlanDoesNotFlagSmallInstanceType(t *testing.T) {
	resources, err := ScanPlan(readTestdata(t, "plan-small-instance.json"))
	if err != nil {
		t.Fatalf("ScanPlan: %v", err)
	}
	flags := FlagCostlyResources(resources, Budget{})
	if len(flags) != 0 {
		t.Errorf("expected t3.micro not to be flagged as large, got %+v", flags)
	}
}

func TestScanPlanExplicitApprovalIsHonored(t *testing.T) {
	resources, err := ScanPlan(readTestdata(t, "plan-nat-gateway.json"))
	if err != nil {
		t.Fatalf("ScanPlan: %v", err)
	}
	budget := Budget{ApprovedCostlyResourceTypes: []string{"aws_nat_gateway"}}
	flags := FlagCostlyResources(resources, budget)
	if len(flags) != 1 || !flags[0].ExplicitlyApproved {
		t.Fatalf("expected the NAT gateway flag to be marked explicitly approved, got %+v", flags)
	}
}

func TestScanPlanExcludesPureDeletes(t *testing.T) {
	resources, err := ScanPlan(readTestdata(t, "plan-delete-only.json"))
	if err != nil {
		t.Fatalf("ScanPlan: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected a pure-delete change to add no future cost, got %+v", resources)
	}
}

func TestScanPlanRejectsMalformedJSON(t *testing.T) {
	_, err := ScanPlan(readTestdata(t, "plan-malformed.json"))
	if err == nil {
		t.Fatal("expected malformed plan JSON to be a hard error, not an empty/silent result")
	}
}
