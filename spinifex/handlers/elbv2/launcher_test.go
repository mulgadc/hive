package handlers_elbv2

import (
	"fmt"
	"sync"
	"testing"
)

// mockSystemInstanceLauncher implements SystemInstanceLauncher for tests.
type mockSystemInstanceLauncher struct {
	mu             sync.Mutex
	launchCalls    []*SystemInstanceInput
	terminateCalls []string
	launchResult   *SystemInstanceOutput
	launchErr      error
	terminateErr   error
	terminateDone  chan struct{} // closed after TerminateSystemInstance completes
}

func (m *mockSystemInstanceLauncher) LaunchSystemInstance(input *SystemInstanceInput) (*SystemInstanceOutput, error) {
	m.launchCalls = append(m.launchCalls, input)
	return m.launchResult, m.launchErr
}

func (m *mockSystemInstanceLauncher) TerminateSystemInstance(instanceID string) error {
	m.mu.Lock()
	m.terminateCalls = append(m.terminateCalls, instanceID)
	m.mu.Unlock()
	if m.terminateDone != nil {
		close(m.terminateDone)
	}
	return m.terminateErr
}

// waitTerminate blocks until TerminateSystemInstance has been called.
func (m *mockSystemInstanceLauncher) waitTerminate() {
	if m.terminateDone != nil {
		<-m.terminateDone
	}
}

func TestSystemInstanceInput_Fields(t *testing.T) {
	input := &SystemInstanceInput{
		InstanceType: "t3.nano",
		ImageID:      "ami-abc123",
		SubnetID:     "subnet-xyz",
		UserData:     "#!/bin/bash\necho hello",
		ENIID:        "eni-test123",
		ENIMac:       "02:00:00:11:22:33",
		ENIIP:        "10.0.1.5",
	}

	if input.InstanceType != "t3.nano" {
		t.Errorf("InstanceType = %q, want %q", input.InstanceType, "t3.nano")
	}
	if input.ImageID != "ami-abc123" {
		t.Errorf("ImageID = %q, want %q", input.ImageID, "ami-abc123")
	}
	if input.SubnetID != "subnet-xyz" {
		t.Errorf("SubnetID = %q, want %q", input.SubnetID, "subnet-xyz")
	}
	if input.ENIID != "eni-test123" {
		t.Errorf("ENIID = %q, want %q", input.ENIID, "eni-test123")
	}
	if input.ENIMac != "02:00:00:11:22:33" {
		t.Errorf("ENIMac = %q, want %q", input.ENIMac, "02:00:00:11:22:33")
	}
	if input.ENIIP != "10.0.1.5" {
		t.Errorf("ENIIP = %q, want %q", input.ENIIP, "10.0.1.5")
	}
}

func TestMockSystemInstanceLauncher_Launch(t *testing.T) {
	mock := &mockSystemInstanceLauncher{
		launchResult: &SystemInstanceOutput{
			InstanceID: "i-test123",
			PrivateIP:  "10.0.1.5",
		},
	}

	input := &SystemInstanceInput{
		InstanceType: "t3.nano",
		ImageID:      "ami-abc123",
		SubnetID:     "subnet-xyz",
		UserData:     "#!/bin/bash\necho hello",
	}

	out, err := mock.LaunchSystemInstance(input)
	if err != nil {
		t.Fatalf("LaunchSystemInstance: %v", err)
	}
	if out.InstanceID != "i-test123" {
		t.Errorf("InstanceID = %q, want %q", out.InstanceID, "i-test123")
	}
	if out.PrivateIP != "10.0.1.5" {
		t.Errorf("PrivateIP = %q, want %q", out.PrivateIP, "10.0.1.5")
	}
	if len(mock.launchCalls) != 1 {
		t.Fatalf("expected 1 launch call, got %d", len(mock.launchCalls))
	}
	if mock.launchCalls[0].InstanceType != "t3.nano" {
		t.Errorf("launch call InstanceType = %q, want %q", mock.launchCalls[0].InstanceType, "t3.nano")
	}
}

func TestMockSystemInstanceLauncher_LaunchError(t *testing.T) {
	mock := &mockSystemInstanceLauncher{
		launchErr: fmt.Errorf("insufficient capacity"),
	}

	_, err := mock.LaunchSystemInstance(&SystemInstanceInput{
		InstanceType: "t3.nano",
		ImageID:      "ami-abc123",
	})
	if err == nil {
		t.Fatal("expected error from LaunchSystemInstance")
	}
	if err.Error() != "insufficient capacity" {
		t.Errorf("error = %q, want %q", err.Error(), "insufficient capacity")
	}
}

func TestMockSystemInstanceLauncher_Terminate(t *testing.T) {
	mock := &mockSystemInstanceLauncher{}

	err := mock.TerminateSystemInstance("i-test123")
	if err != nil {
		t.Fatalf("TerminateSystemInstance: %v", err)
	}
	if len(mock.terminateCalls) != 1 {
		t.Fatalf("expected 1 terminate call, got %d", len(mock.terminateCalls))
	}
	if mock.terminateCalls[0] != "i-test123" {
		t.Errorf("terminate call = %q, want %q", mock.terminateCalls[0], "i-test123")
	}
}

func TestMockSystemInstanceLauncher_TerminateError(t *testing.T) {
	mock := &mockSystemInstanceLauncher{
		terminateErr: fmt.Errorf("instance not found"),
	}

	err := mock.TerminateSystemInstance("i-nonexistent")
	if err == nil {
		t.Fatal("expected error from TerminateSystemInstance")
	}
}

func TestMockSystemInstanceLauncher_WithPreCreatedENI(t *testing.T) {
	mock := &mockSystemInstanceLauncher{
		launchResult: &SystemInstanceOutput{
			InstanceID: "i-alb123",
			PrivateIP:  "10.201.1.6",
		},
	}

	input := &SystemInstanceInput{
		InstanceType: "t3.nano",
		ImageID:      "ami-abc123",
		SubnetID:     "subnet-vpc1",
		UserData:     "#!/bin/bash\napt-get install haproxy",
		ENIID:        "eni-alb456",
		ENIMac:       "02:00:00:aa:bb:cc",
		ENIIP:        "10.201.1.6",
	}

	out, err := mock.LaunchSystemInstance(input)
	if err != nil {
		t.Fatalf("LaunchSystemInstance: %v", err)
	}
	if out.InstanceID != "i-alb123" {
		t.Errorf("InstanceID = %q, want %q", out.InstanceID, "i-alb123")
	}
	if mock.launchCalls[0].ENIID != "eni-alb456" {
		t.Errorf("ENIID = %q, want %q", mock.launchCalls[0].ENIID, "eni-alb456")
	}
}

// Verify the interface is satisfied by the mock at compile time.
var _ SystemInstanceLauncher = (*mockSystemInstanceLauncher)(nil)
