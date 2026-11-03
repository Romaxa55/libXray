package xray

import (
	"encoding/json"
	"testing"
)

func TestGetBuildInfoVersions(t *testing.T) {
	oldV, oldX, oldL := coreVersion, xrayCoreCommit, libxrayCommit
	t.Cleanup(func() { coreVersion, xrayCoreCommit, libxrayCommit = oldV, oldX, oldL })
	coreVersion = "v26.11.4-xray-59949ab"
	xrayCoreCommit = "59949ab0c3097a50a6952baae88f04d176ed3fd4"
	libxrayCommit = "0cafcb73c5cc8271af1d1c9155cb0136fb6b0b70"

	var info BuildInfo
	if err := json.Unmarshal([]byte(GetBuildInfo()), &info); err != nil {
		t.Fatalf("GetBuildInfo is not JSON: %v", err)
	}
	if info.CoreVersion != coreVersion || info.XrayCoreCommit != xrayCoreCommit {
		t.Fatalf("versions not reported: %+v", info)
	}
	if info.LibxrayCommit == "" {
		t.Fatal("libxray_commit is empty")
	}
	if !info.Features["pr5805_balancer_dialer"] {
		t.Fatal("feature map lost")
	}
}
