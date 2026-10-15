package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestRedfish(t *testing.T) (*httptest.Server, *bootdevStore) {
	t.Helper()
	cfg, _ := testConfig(t)
	boot := newBootdevStore(cfg.BootdevPath)
	rf := &redfishServer{cfg: cfg, chassis: newExecHAL(cfg, boot).Chassis(), boot: boot}
	srv := httptest.NewServer(rf.handler())
	t.Cleanup(srv.Close)
	return srv, boot
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

func fileTarget(t *testing.T, boot *bootdevStore) (string, bool) {
	t.Helper()
	f, err := boot.read()
	if err != nil {
		t.Fatalf("read bootdev file: %v", err)
	}
	return f.Target, f.Persistent
}

func TestRedfishSystemsRequireAuth(t *testing.T) {
	srv, _ := newTestRedfish(t)
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
	srv, boot := newTestRedfish(t)

	body := `{"Boot": {"BootSourceOverrideTarget": "Pxe", "BootSourceOverrideEnabled": "Once"}}`
	resp := do(t, "PATCH", srv.URL+"/redfish/v1/Systems/1", body, true)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204", resp.StatusCode)
	}

	target, persistent := fileTarget(t, boot)
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
	srv, boot := newTestRedfish(t)

	// Setting a target while the override is disabled stores the target
	// without enabling it: the file keeps target none, and the resource
	// reports the stored target with Enabled still Disabled.
	body := `{"Boot": {"BootSourceOverrideTarget": "Pxe"}}`
	resp := do(t, "PATCH", srv.URL+"/redfish/v1/Systems/1", body, true)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204", resp.StatusCode)
	}
	if target, _ := fileTarget(t, boot); target != "none" {
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
	if target, persistent := fileTarget(t, boot); target != "pxe" || persistent {
		t.Fatalf("boot state = %s persistent=%t, want pxe persistent=false", target, persistent)
	}

	// Disabling keeps the stored target visible but resets the file.
	body = `{"Boot": {"BootSourceOverrideEnabled": "Disabled"}}`
	if resp = do(t, "PATCH", srv.URL+"/redfish/v1/Systems/1", body, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("disable status = %d, want 204", resp.StatusCode)
	}
	if target, _ := fileTarget(t, boot); target != "none" {
		t.Fatalf("file target after disable = %s, want none", target)
	}
}

// After the file's reader resets a one-shot override, patching only the
// target stores it without enabling the override again.
func TestRedfishBootPatchAfterReaderResetStaysDisabled(t *testing.T) {
	srv, boot := newTestRedfish(t)

	body := `{"Boot": {"BootSourceOverrideTarget": "Pxe", "BootSourceOverrideEnabled": "Once"}}`
	if resp := do(t, "PATCH", srv.URL+"/redfish/v1/Systems/1", body, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204", resp.StatusCode)
	}
	if err := os.WriteFile(boot.path, []byte(`{"target": "none", "persistent": false}`), 0o644); err != nil {
		t.Fatal(err)
	}

	body = `{"Boot": {"BootSourceOverrideTarget": "Hdd"}}`
	if resp := do(t, "PATCH", srv.URL+"/redfish/v1/Systems/1", body, true); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("patch status = %d, want 204", resp.StatusCode)
	}
	if target, _ := fileTarget(t, boot); target != "none" {
		t.Fatalf("file target = %s, want none", target)
	}
	resp := do(t, "GET", srv.URL+"/redfish/v1/Systems/1", "", true)
	var sys computerSystem
	if err := json.NewDecoder(resp.Body).Decode(&sys); err != nil {
		t.Fatal(err)
	}
	if sys.Boot.BootSourceOverrideTarget != "Hdd" || sys.Boot.BootSourceOverrideEnabled != "Disabled" {
		t.Fatalf("Boot = %+v, want Hdd Disabled", sys.Boot)
	}
}

func TestRedfishBootPatchRejectsUnknownTarget(t *testing.T) {
	srv, _ := newTestRedfish(t)
	body := `{"Boot": {"BootSourceOverrideTarget": "Usb"}}`
	resp := do(t, "PATCH", srv.URL+"/redfish/v1/Systems/1", body, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("patch status = %d, want 400", resp.StatusCode)
	}
}

// A reset runs to completion after the client disconnects.
func TestRedfishResetSurvivesClientDisconnect(t *testing.T) {
	cfg, _ := testConfig(t)
	done := filepath.Join(t.TempDir(), "done")
	chassis := &execChassis{
		commands: Commands{PowerOn: "sleep 1; touch " + done},
		timeout:  cfg.CommandTimeout(),
		boot:     newBootdevStore(cfg.BootdevPath),
	}
	rf := &redfishServer{cfg: cfg, chassis: chassis, boot: chassis.boot}
	srv := httptest.NewServer(rf.handler())
	t.Cleanup(srv.Close)

	req, err := http.NewRequest("POST", srv.URL+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", strings.NewReader(`{"ResetType": "On"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("admin", "secret")
	client := &http.Client{Timeout: 200 * time.Millisecond}
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("request completed before the client timeout")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(done); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("power_on did not complete after the client disconnected")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
