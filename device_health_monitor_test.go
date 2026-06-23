package main

import "testing"

func TestDeviceHealthCheckSkipsWhenSyncGuardLocked(t *testing.T) {
	globalDeviceHealthMonitor = &deviceHealthMonitor{}

	deviceID := uint32(3)
	globalDeviceHealthMonitor.cfg = deviceHealthConfig{
		Enabled: true,
		Port:    5010,
	}
	globalDeviceHealthMonitor.states = map[uint32]deviceHealthState{
		deviceID: {
			DeviceID: deviceID,
			IP:       "192.0.2.10",
		},
	}

	guard := getDeviceSyncGuard(deviceID)
	guard.Lock()
	defer guard.Unlock()

	globalDeviceHealthMonitor.checkOne(deviceID)

	state := globalDeviceHealthMonitor.states[deviceID]
	if state.ReachabilityKnown {
		t.Fatalf("expected health probe to be skipped while sync lock is held")
	}
}
