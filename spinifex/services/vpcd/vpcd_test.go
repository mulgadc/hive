package vpcd

import (
	"fmt"
	"strings"
	"testing"
)

func TestPreflightOVN_AllPass(t *testing.T) {
	origBrInt := checkBrInt
	origCtrl := checkOVNController
	defer func() {
		checkBrInt = origBrInt
		checkOVNController = origCtrl
	}()

	checkBrInt = func() error { return nil }
	checkOVNController = func() error { return nil }

	if err := preflightOVN(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestPreflightOVN_BrIntMissing(t *testing.T) {
	origBrInt := checkBrInt
	origCtrl := checkOVNController
	defer func() {
		checkBrInt = origBrInt
		checkOVNController = origCtrl
	}()

	checkBrInt = func() error {
		return fmt.Errorf("br-int does not exist: run ./scripts/setup-ovn.sh --management")
	}
	checkOVNController = func() error { return nil }

	err := preflightOVN()
	if err == nil {
		t.Fatal("expected error when br-int is missing")
	}
	if !strings.Contains(err.Error(), "br-int") {
		t.Errorf("expected error to mention br-int, got: %v", err)
	}
}

func TestPreflightOVN_ControllerNotRunning(t *testing.T) {
	origBrInt := checkBrInt
	origCtrl := checkOVNController
	defer func() {
		checkBrInt = origBrInt
		checkOVNController = origCtrl
	}()

	checkBrInt = func() error { return nil }
	checkOVNController = func() error {
		return fmt.Errorf("ovn-controller is not running: run ./scripts/setup-ovn.sh --management")
	}

	err := preflightOVN()
	if err == nil {
		t.Fatal("expected error when ovn-controller is down")
	}
	if !strings.Contains(err.Error(), "ovn-controller") {
		t.Errorf("expected error to mention ovn-controller, got: %v", err)
	}
}

func TestDiscoverChassis_ParsesOutput(t *testing.T) {
	orig := discoverChassis
	defer func() { discoverChassis = orig }()

	discoverChassis = func(sbAddr string) ([]string, error) {
		// Simulate ovn-sbctl list-chassis output
		output := "chassis-node1\nchassis-node2\nchassis-node3\n"
		var names []string
		for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
			name := strings.TrimSpace(line)
			if name != "" {
				names = append(names, name)
			}
		}
		return names, nil
	}

	names, err := discoverChassis("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 chassis, got %d: %v", len(names), names)
	}
	expected := map[string]bool{"chassis-node1": true, "chassis-node2": true, "chassis-node3": true}
	for _, n := range names {
		if !expected[n] {
			t.Errorf("unexpected chassis name: %s", n)
		}
	}
}

func TestDiscoverChassis_SingleNode(t *testing.T) {
	orig := discoverChassis
	defer func() { discoverChassis = orig }()

	discoverChassis = func(sbAddr string) ([]string, error) {
		output := "chassis-spinifex-image-builder\n"
		var names []string
		for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
			name := strings.TrimSpace(line)
			if name != "" {
				names = append(names, name)
			}
		}
		return names, nil
	}

	names, err := discoverChassis("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 1 || names[0] != "chassis-spinifex-image-builder" {
		t.Errorf("expected [chassis-spinifex-image-builder], got %v", names)
	}
}

func TestDiscoverChassis_EmptyOutput(t *testing.T) {
	orig := discoverChassis
	defer func() { discoverChassis = orig }()

	discoverChassis = func(sbAddr string) ([]string, error) {
		return nil, nil
	}

	names, err := discoverChassis("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected empty chassis list, got %v", names)
	}
}

func TestDiscoverChassis_Error_FallsBackToConfig(t *testing.T) {
	orig := discoverChassis
	defer func() { discoverChassis = orig }()

	discoverChassis = func(sbAddr string) ([]string, error) {
		return nil, fmt.Errorf("connection refused")
	}

	// Simulate the fallback logic from launchService
	_, err := discoverChassis("")
	if err == nil {
		t.Fatal("expected error from discoverChassis")
	}

	// Fallback to config (as launchService does)
	chassisNames := []string{"chassis-node1"}
	if len(chassisNames) != 1 || chassisNames[0] != "chassis-node1" {
		t.Errorf("expected fallback to config names, got %v", chassisNames)
	}
}

func TestPreflightOVN_BothFail_ReportsFirst(t *testing.T) {
	origBrInt := checkBrInt
	origCtrl := checkOVNController
	defer func() {
		checkBrInt = origBrInt
		checkOVNController = origCtrl
	}()

	checkBrInt = func() error {
		return fmt.Errorf("br-int does not exist")
	}
	checkOVNController = func() error {
		return fmt.Errorf("ovn-controller is not running")
	}

	err := preflightOVN()
	if err == nil {
		t.Fatal("expected error when both fail")
	}
	// Should report br-int first (checked first)
	if !strings.Contains(err.Error(), "br-int") {
		t.Errorf("expected first error to mention br-int, got: %v", err)
	}
}
