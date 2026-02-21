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
