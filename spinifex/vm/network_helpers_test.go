package vm

import (
	"errors"
	"strings"
	"testing"
)

// fakeNetworkPlumber records calls so tests can assert per-ENI behaviour.
type fakeNetworkPlumber struct {
	setupCalls   []fakeSetupCall
	cleanupCalls []string
	setupErr     error
}

type fakeSetupCall struct {
	ENIID string
	MAC   string
}

func (p *fakeNetworkPlumber) SetupTapDevice(eniID, mac string) error {
	p.setupCalls = append(p.setupCalls, fakeSetupCall{ENIID: eniID, MAC: mac})
	return p.setupErr
}

func (p *fakeNetworkPlumber) CleanupTapDevice(eniID string) error {
	p.cleanupCalls = append(p.cleanupCalls, eniID)
	return nil
}

var _ NetworkPlumber = (*fakeNetworkPlumber)(nil)

func TestSetupExtraENINICs_AppendsOnePerExtra(t *testing.T) {
	plumber := &fakeNetworkPlumber{}
	m := NewManagerWithDeps(Deps{NetworkPlumber: plumber})
	instance := &VM{
		ID: "i-multi",
		ExtraENIs: []ExtraENI{
			{ENIID: "eni-aaa", ENIMac: "02:00:00:aa:aa:aa", ENIIP: "10.0.1.4", SubnetID: "subnet-a"},
			{ENIID: "eni-bbb", ENIMac: "02:00:00:bb:bb:bb", ENIIP: "10.0.2.4", SubnetID: "subnet-b"},
		},
	}

	if err := m.setupExtraENINICs(instance); err != nil {
		t.Fatalf("setupExtraENINICs failed: %v", err)
	}

	if len(plumber.setupCalls) != 2 {
		t.Fatalf("expected 2 SetupTapDevice calls, got %d", len(plumber.setupCalls))
	}
	if plumber.setupCalls[0].ENIID != "eni-aaa" || plumber.setupCalls[0].MAC != "02:00:00:aa:aa:aa" {
		t.Errorf("first setup call = %+v, want eni-aaa/02:00:00:aa:aa:aa", plumber.setupCalls[0])
	}
	if plumber.setupCalls[1].ENIID != "eni-bbb" || plumber.setupCalls[1].MAC != "02:00:00:bb:bb:bb" {
		t.Errorf("second setup call = %+v, want eni-bbb/02:00:00:bb:bb:bb", plumber.setupCalls[1])
	}

	if len(instance.Config.NetDevs) != 2 || len(instance.Config.Devices) != 2 {
		t.Fatalf("expected 2 netdevs + 2 devices, got %d + %d",
			len(instance.Config.NetDevs), len(instance.Config.Devices))
	}
	if !strings.Contains(instance.Config.NetDevs[0].Value, "id=net1") {
		t.Errorf("netdev[0] = %q, want id=net1", instance.Config.NetDevs[0].Value)
	}
	if !strings.Contains(instance.Config.NetDevs[1].Value, "id=net2") {
		t.Errorf("netdev[1] = %q, want id=net2", instance.Config.NetDevs[1].Value)
	}
	if !strings.Contains(instance.Config.Devices[0].Value, "mac=02:00:00:aa:aa:aa") {
		t.Errorf("device[0] = %q, missing primary MAC", instance.Config.Devices[0].Value)
	}
	if !strings.Contains(instance.Config.Devices[1].Value, "mac=02:00:00:bb:bb:bb") {
		t.Errorf("device[1] = %q, missing second MAC", instance.Config.Devices[1].Value)
	}
}

func TestSetupExtraENINICs_NoExtras_NoOp(t *testing.T) {
	plumber := &fakeNetworkPlumber{}
	m := NewManagerWithDeps(Deps{NetworkPlumber: plumber})
	instance := &VM{ID: "i-single"}

	if err := m.setupExtraENINICs(instance); err != nil {
		t.Fatalf("setupExtraENINICs failed: %v", err)
	}
	if len(plumber.setupCalls) != 0 {
		t.Errorf("expected zero setup calls for no extras, got %d", len(plumber.setupCalls))
	}
	if len(instance.Config.NetDevs) != 0 || len(instance.Config.Devices) != 0 {
		t.Errorf("expected no netdevs/devices, got %d/%d",
			len(instance.Config.NetDevs), len(instance.Config.Devices))
	}
}

func TestSetupExtraENINICs_TapSetupErrorReturns(t *testing.T) {
	plumber := &fakeNetworkPlumber{setupErr: errors.New("simulated tap failure")}
	m := NewManagerWithDeps(Deps{NetworkPlumber: plumber})
	instance := &VM{
		ID: "i-multi-err",
		ExtraENIs: []ExtraENI{
			{ENIID: "eni-aaa", ENIMac: "02:00:00:aa:aa:aa"},
			{ENIID: "eni-bbb", ENIMac: "02:00:00:bb:bb:bb"},
		},
	}

	err := m.setupExtraENINICs(instance)
	if err == nil {
		t.Fatal("expected error from failing tap setup, got nil")
	}
	if !strings.Contains(err.Error(), "eni-aaa") {
		t.Errorf("error = %v, want it to mention the failing ENI", err)
	}
	if len(plumber.setupCalls) != 1 {
		t.Errorf("expected 1 setup call before bailout, got %d", len(plumber.setupCalls))
	}
	if len(instance.Config.NetDevs) != 0 {
		t.Errorf("expected no netdevs on failure, got %d", len(instance.Config.NetDevs))
	}
}

func TestSetupExtraENINICs_NilPlumber_NoOp(t *testing.T) {
	m := NewManagerWithDeps(Deps{})
	instance := &VM{
		ID: "i-no-plumber",
		ExtraENIs: []ExtraENI{
			{ENIID: "eni-aaa", ENIMac: "02:00:00:aa:aa:aa"},
		},
	}
	if err := m.setupExtraENINICs(instance); err != nil {
		t.Fatalf("setupExtraENINICs without plumber should be a no-op, got %v", err)
	}
	if len(instance.Config.NetDevs) != 0 {
		t.Errorf("expected no netdevs without plumber, got %d", len(instance.Config.NetDevs))
	}
}
