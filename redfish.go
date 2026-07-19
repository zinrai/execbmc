package main

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/bougou/go-ipmi/pkg/hal"
	"github.com/bougou/go-ipmi/pkg/types"
)

const systemID = "1"

// Redfish boot source names mapped to bootdev file targets.
var redfishToTarget = map[string]string{
	"None": targetNone,
	"Pxe":  targetPXE,
	"Hdd":  targetDisk,
	"Cd":   targetCDROM,
}

var targetToRedfish = map[string]string{
	targetNone:  "None",
	targetPXE:   "Pxe",
	targetDisk:  "Hdd",
	targetCDROM: "Cd",
}

var allowableTargets = []string{"None", "Pxe", "Hdd", "Cd"}
var allowableResets = []string{"On", "ForceOff", "GracefulShutdown", "ForceRestart", "PowerCycle"}

// redfishServer serves the Redfish subset for one system.
type redfishServer struct {
	cfg     *Config
	chassis hal.ChassisHAL
	boot    *bootdevStore
}

// serveRedfish runs the Redfish HTTP server until ctx is cancelled.
func serveRedfish(ctx context.Context, cfg *Config, chassis hal.ChassisHAL, boot *bootdevStore) error {
	rf := &redfishServer{cfg: cfg, chassis: chassis, boot: boot}

	srv := &http.Server{
		Addr:    cfg.Redfish.Listen,
		Handler: rf.handler(),
	}

	if cfg.Redfish.TLSEnabled() && cfg.Redfish.CertFile == "" {
		cert, err := selfSignedCertificate(cfg.Name)
		if err != nil {
			return fmt.Errorf("generate certificate: %w", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	}

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	var err error
	if cfg.Redfish.TLSEnabled() {
		slog.Info("listening", "component", "redfish", "addr", cfg.Redfish.Listen, "tls", true)
		err = srv.ListenAndServeTLS(cfg.Redfish.CertFile, cfg.Redfish.KeyFile)
	} else {
		slog.Info("listening", "component", "redfish", "addr", cfg.Redfish.Listen, "tls", false)
		err = srv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("redfish serve: %w", err)
	}
	return nil
}

// handler builds the route table. The service root is served without
// authentication as required by the Redfish specification; everything else
// requires HTTP Basic authentication.
func (s *redfishServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /redfish/v1", s.handleServiceRoot)
	mux.HandleFunc("GET /redfish/v1/{$}", s.handleServiceRoot)
	mux.HandleFunc("GET /redfish/v1/Systems", s.auth(s.handleSystems))
	mux.HandleFunc("GET /redfish/v1/Systems/{id}", s.auth(s.handleSystemGet))
	mux.HandleFunc("PATCH /redfish/v1/Systems/{id}", s.auth(s.handleSystemPatch))
	mux.HandleFunc("POST /redfish/v1/Systems/{id}/Actions/ComputerSystem.Reset", s.auth(s.handleSystemReset))
	return logRequests(mux)
}

// statusRecorder captures the response status for request logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// logRequests logs every Redfish request as one line.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("redfish",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"remote", r.RemoteAddr,
		)
	})
}

// auth wraps a handler with HTTP Basic authentication.
func (s *redfishServer) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.Username)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.Password)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="execbmc"`)
			s.writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next(w, r)
	}
}

func (s *redfishServer) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *redfishServer) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, redfishError{
		Error: redfishErrorBody{
			Code:    "Base.1.0.GeneralError",
			Message: message,
		},
	})
}

func (s *redfishServer) handleServiceRoot(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, serviceRoot{
		ODataID:        "/redfish/v1/",
		ODataType:      "#ServiceRoot.v1_5_0.ServiceRoot",
		ID:             "RootService",
		Name:           "execbmc",
		RedfishVersion: "1.9.0",
		Systems:        odataRef{ODataID: "/redfish/v1/Systems"},
	})
}

func (s *redfishServer) handleSystems(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, systemCollection{
		ODataID:      "/redfish/v1/Systems",
		ODataType:    "#ComputerSystemCollection.ComputerSystemCollection",
		Name:         "Computer System Collection",
		MembersCount: 1,
		Members:      []odataRef{{ODataID: "/redfish/v1/Systems/" + systemID}},
	})
}

// currentBoot reads the shared boot state as a Redfish Boot object. The
// target and the enable state are reported independently: a stored target
// remains visible while the override is disabled.
func (s *redfishServer) currentBoot() systemBoot {
	flags := s.boot.Get()
	target, err := selectorToTarget(flags.BootDeviceSelector)
	if err != nil {
		target = targetNone
	}
	boot := systemBoot{
		BootSourceOverrideTarget:          targetToRedfish[target],
		BootSourceOverrideTargetAllowable: allowableTargets,
	}
	switch {
	case !flags.BootFlagsValid:
		boot.BootSourceOverrideEnabled = "Disabled"
	case flags.Persist:
		boot.BootSourceOverrideEnabled = "Continuous"
	default:
		boot.BootSourceOverrideEnabled = "Once"
	}
	return boot
}

func (s *redfishServer) handleSystemGet(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != systemID {
		s.writeError(w, http.StatusNotFound, "system not found")
		return
	}
	on, err := s.chassis.PowerState(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	powerState := "Off"
	if on {
		powerState = "On"
	}
	s.writeJSON(w, http.StatusOK, computerSystem{
		ODataID:    "/redfish/v1/Systems/" + systemID,
		ODataType:  "#ComputerSystem.v1_13_0.ComputerSystem",
		ID:         systemID,
		Name:       s.cfg.Name,
		SystemType: "Physical",
		PowerState: powerState,
		Boot:       s.currentBoot(),
		Actions: systemActions{
			Reset: systemResetAction{
				Target:             "/redfish/v1/Systems/" + systemID + "/Actions/ComputerSystem.Reset",
				ResetTypeAllowable: allowableResets,
			},
		},
	})
}

func (s *redfishServer) handleSystemPatch(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != systemID {
		s.writeError(w, http.StatusNotFound, "system not found")
		return
	}
	var patch systemPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if patch.Boot == nil {
		s.writeError(w, http.StatusBadRequest, "only the Boot object can be patched")
		return
	}

	// PATCH is a partial update: properties absent from the request keep
	// their current value, matching the Redfish specification. Setting a
	// target while the override is disabled stores the target without
	// enabling it, exactly like a spec-conforming BMC.
	flags := *s.boot.Get()
	if patch.Boot.BootSourceOverrideTarget != nil {
		t, ok := redfishToTarget[*patch.Boot.BootSourceOverrideTarget]
		if !ok {
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported BootSourceOverrideTarget %q", *patch.Boot.BootSourceOverrideTarget))
			return
		}
		flags.BootDeviceSelector, _ = targetToSelector(t)
	}
	if patch.Boot.BootSourceOverrideEnabled != nil {
		switch *patch.Boot.BootSourceOverrideEnabled {
		case "Disabled":
			flags.BootFlagsValid = false
		case "Once":
			flags.BootFlagsValid = true
			flags.Persist = false
		case "Continuous":
			flags.BootFlagsValid = true
			flags.Persist = true
		default:
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported BootSourceOverrideEnabled %q", *patch.Boot.BootSourceOverrideEnabled))
			return
		}
	}
	if flags.BootFlagsValid && flags.BootDeviceSelector == types.BootDeviceSelectorNoOverride {
		s.writeError(w, http.StatusBadRequest, "BootSourceOverrideTarget is required to enable an override")
		return
	}

	if err := s.boot.Set(&flags); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	target, persistent := s.boot.Target()
	slog.Info("boot_flags", "source", "redfish", "target", target, "persistent", persistent)
	w.WriteHeader(http.StatusNoContent)
}

func (s *redfishServer) handleSystemReset(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != systemID {
		s.writeError(w, http.StatusNotFound, "system not found")
		return
	}
	var req resetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	var err error
	switch req.ResetType {
	case "On":
		err = s.chassis.SetPower(r.Context(), true)
	case "ForceOff":
		err = s.chassis.SetPower(r.Context(), false)
	case "GracefulShutdown":
		err = s.chassis.WarmReset(r.Context())
	case "ForceRestart":
		err = s.chassis.ColdReset(r.Context())
	case "PowerCycle":
		err = s.chassis.PowerCycle(r.Context())
	default:
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported ResetType %q", req.ResetType))
		return
	}
	if err != nil {
		if errors.Is(err, hal.ErrNotSupported) {
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("ResetType %q is not supported by this configuration", req.ResetType))
			return
		}
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
