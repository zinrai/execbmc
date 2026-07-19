package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bougou/go-ipmi/pkg/hal"
)

// fakeChassis records calls for Redfish handler tests.
type fakeChassis struct {
	execChassis
	on       bool
	lastCall string
}

func (f *fakeChassis) PowerState(ctx context.Context) (bool, error) { return f.on, nil }
func (f *fakeChassis) SetPower(ctx context.Context, on bool) error {
	f.on = on
	if on {
		f.lastCall = "on"
	} else {
		f.lastCall = "off"
	}
	return nil
}
func (f *fakeChassis) PowerCycle(ctx context.Context) error { f.lastCall = "cycle"; return nil }
func (f *fakeChassis) ColdReset(ctx context.Context) error  { f.lastCall = "cold"; return nil }
func (f *fakeChassis) WarmReset(ctx context.Context) error  { return hal.ErrNotSupported }

func newTestRedfish(t *testing.T) (*httptest.Server, *fakeChassis, *bootdevStore) {
	t.Helper()
	cfg, _ := testConfig(t)
	boot := newBootdevStore(cfg.BootdevPath)
	chassis := &fakeChassis{}
	rf := &redfishServer{cfg: cfg, chassis: chassis, boot: boot}
	srv := httptest.NewServer(rf.handler())
	t.Cleanup(srv.Close)
	return srv, chassis, boot
}

func do(t *testing.T, method, url, body string, auth bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if auth {
		req.SetBasicAuth("admin", "secret")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestRedfishServiceRootUnauthenticated(t *testing.T) {
	srv, _, _ := newTestRedfish(t)
	resp := do(t, "GET", srv.URL+"/redfish/v1/", "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("service root status = %d, want 200", resp.StatusCode)
	}
}

func TestRedfishSystemsRequireAuth(t *testing.T) {
	srv, _, _ := newTestRedfish(t)
	resp := do(t, "GET", srv.URL+"/redfish/v1/Systems", "", false)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", resp.StatusCode)
	}
	resp = do(t, "GET", srv.URL+"/redfish/v1/Systems", "", true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200", resp.StatusCode)
	}
}

func TestRedfishBootPatchWritesBootdev(t *testing.T) {
	srv, _, boot := newTestRedfish(t)

	body := `{"Boot": {"BootSourceOverrideTarget": "Pxe", "BootSourceOverrideEnabled": "Once"}}`
	resp := do(t, "PATCH", srv.URL+"/redfish/v1/Systems/1", body, true)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204", resp.StatusCode)
	}

	target, persistent := boot.Target()
	if target != "pxe" || persistent {
		t.Fatalf("boot state = %s persistent=%t, want pxe persistent=false", target, persistent)
	}

	// The system resource reflects the override.
	resp = do(t, "GET", srv.URL+"/redfish/v1/Systems/1", "", true)
	var sys computerSystem
	if err := json.NewDecoder(resp.Body).Decode(&sys); err != nil {
		t.Fatal(err)
	}
	if sys.Boot.BootSourceOverrideTarget != "Pxe" || sys.Boot.BootSourceOverrideEnabled != "Once" {
		t.Fatalf("Boot = %+v, want Pxe Once", sys.Boot)
	}
}

func TestRedfishBootPatchIsPartialUpdate(t *testing.T) {
	srv, _, boot := newTestRedfish(t)

	// Setting a target while the override is disabled stores the target
	// without enabling it: the file keeps target none, and the resource
	// reports the stored target with Enabled still Disabled.
	body := `{"Boot": {"BootSourceOverrideTarget": "Pxe"}}`
	resp := do(t, "PATCH", srv.URL+"/redfish/v1/Systems/1", body, true)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204", resp.StatusCode)
	}
	if target, _ := boot.Target(); target != "none" {
		t.Fatalf("file target = %s, want none while disabled", target)
	}
	resp = do(t, "GET", srv.URL+"/redfish/v1/Systems/1", "", true)
	var sys computerSystem
	if err := json.NewDecoder(resp.Body).Decode(&sys); err != nil {
		t.Fatal(err)
	}
	if sys.Boot.BootSourceOverrideTarget != "Pxe" || sys.Boot.BootSourceOverrideEnabled != "Disabled" {
		t.Fatalf("Boot = %+v, want Pxe Disabled", sys.Boot)
	}

	// Enabling afterwards, without repeating the target, activates the
	// stored target.
	body = `{"Boot": {"BootSourceOverrideEnabled": "Once"}}`
	resp = do(t, "PATCH", srv.URL+"/redfish/v1/Systems/1", body, true)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("enable status = %d, want 204", resp.StatusCode)
	}
	if target, persistent := boot.Target(); target != "pxe" || persistent {
		t.Fatalf("boot state = %s persistent=%t, want pxe persistent=false", target, persistent)
	}

	// Disabling keeps the stored target visible but resets the file.
	body = `{"Boot": {"BootSourceOverrideEnabled": "Disabled"}}`
	if resp = do(t, "PATCH", srv.URL+"/redfish/v1/Systems/1", body, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("disable status = %d, want 204", resp.StatusCode)
	}
	if target, _ := boot.Target(); target != "none" {
		t.Fatalf("file target after disable = %s, want none", target)
	}
}

func TestRedfishBootPatchRejectsUnknownTarget(t *testing.T) {
	srv, _, _ := newTestRedfish(t)
	body := `{"Boot": {"BootSourceOverrideTarget": "Usb"}}`
	resp := do(t, "PATCH", srv.URL+"/redfish/v1/Systems/1", body, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("patch status = %d, want 400", resp.StatusCode)
	}
}

func TestRedfishReset(t *testing.T) {
	srv, chassis, _ := newTestRedfish(t)

	resp := do(t, "POST", srv.URL+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", `{"ResetType": "On"}`, true)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset status = %d, want 204", resp.StatusCode)
	}
	if chassis.lastCall != "on" {
		t.Fatalf("lastCall = %s, want on", chassis.lastCall)
	}

	// GracefulShutdown maps to WarmReset, which the fake reports unsupported.
	resp = do(t, "POST", srv.URL+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", `{"ResetType": "GracefulShutdown"}`, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("graceful status = %d, want 400", resp.StatusCode)
	}
}
