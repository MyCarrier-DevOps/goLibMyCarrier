package slippy

import (
	"strings"
	"testing"
)

func TestStepStatusEnumDefinitionIncludesHeld(t *testing.T) {
	enumDef := stepStatusEnumDefinition("\t")
	if !strings.Contains(enumDef, "'held'") {
		t.Fatalf("expected enum definition to include 'held', got: %s", enumDef)
	}
}

func TestGenerateStepColumnEnsurerIncludesModifyAndHeld(t *testing.T) {
	manager := &DynamicMigrationManager{database: "ci"}
	ensurer := manager.generateStepColumnEnsurer(StepConfig{Name: "dev_deploy"})

	if !strings.Contains(ensurer.SQL, "MODIFY COLUMN dev_deploy_status") {
		t.Fatalf("expected ensurer SQL to modify the step column, got: %s", ensurer.SQL)
	}

	if !strings.Contains(ensurer.SQL, "'held'") {
		t.Fatalf("expected ensurer SQL to include 'held' status, got: %s", ensurer.SQL)
	}
}
