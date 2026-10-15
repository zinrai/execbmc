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

var errUnsupportedResetType = errors.New("unsupported ResetType")

var allowableTargets = []string{"None", "Pxe", "Hdd", "Cd"}
var allowableResets = []string{"On", "ForceOff", "GracefulShutdown", "ForceRestart", "PowerCycle"}

type redfishServer struct {
	cfg     *Config
	chassis hal.ChassisHAL
	boot    *bootdevStore
}

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

func (s *redfishServer) handler() http.Handler {
	mux := http.NewServeMux()
	// Not behind auth: the Redfish specification requires an open service root.
	mux.HandleFunc("GET /redfish/v1", s.handleServiceRoot)
	mux.HandleFunc("GET /redfish/v1/{$}", s.handleServiceRoot)
	mux.HandleFunc("GET /redfish/v1/Systems", s.auth(s.handleSystems))
	mux.HandleFunc("GET /redfish/v1/Systems/{id}", s.auth(s.handleSystemGet))
	mux.HandleFunc("PATCH /redfish/v1/Systems/{id}", s.auth(s.handleSystemPatch))
	mux.HandleFunc("POST /redfish/v1/Systems/{id}/Actions/ComputerSystem.Reset", s.auth(s.handleSystemReset))
	return logRequests(mux)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

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

func (s *redfishServer) currentBoot() (systemBoot, error) {
	flags, err := s.boot.Get()
	if err != nil {
		return systemBoot{}, err
	}
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
	return boot, nil
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
	boot, err := s.currentBoot()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, computerSystem{
		ODataID:    "/redfish/v1/Systems/" + systemID,
		ODataType:  "#ComputerSystem.v1_13_0.ComputerSystem",
		ID:         systemID,
		Name:       s.cfg.Name,
		SystemType: "Physical",
		PowerState: powerState,
		Boot:       boot,
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

	current, err := s.boot.Get()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	flags, err := patch.Boot.apply(*current)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	f, err := s.boot.Set(&flags)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("boot_flags", "source", "redfish", "target", f.Target, "persistent", f.Persistent)
	w.WriteHeader(http.StatusNoContent)
}

// Not a replacement: properties absent from the patch keep their value, and
// a target set while the override is disabled is stored without enabling
// it, as the Redfish specification requires.
func (p bootPatch) apply(flags types.BootOptionParam_BootFlags) (types.BootOptionParam_BootFlags, error) {
	if p.BootSourceOverrideTarget != nil {
		t, ok := redfishToTarget[*p.BootSourceOverrideTarget]
		if !ok {
			return flags, fmt.Errorf("unsupported BootSourceOverrideTarget %q", *p.BootSourceOverrideTarget)
		}
		flags.BootDeviceSelector, _ = targetToSelector(t)
	}
	if p.BootSourceOverrideEnabled != nil {
		switch *p.BootSourceOverrideEnabled {
		case "Disabled":
			flags.BootFlagsValid = false
		case "Once":
			flags.BootFlagsValid = true
			flags.Persist = false
		case "Continuous":
			flags.BootFlagsValid = true
			flags.Persist = true
		default:
			return flags, fmt.Errorf("unsupported BootSourceOverrideEnabled %q", *p.BootSourceOverrideEnabled)
		}
	}
	if flags.BootFlagsValid && flags.BootDeviceSelector == types.BootDeviceSelectorNoOverride {
		return flags, errors.New("BootSourceOverrideTarget is required to enable an override")
	}
	return flags, nil
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

	// Not the request context: a client that disconnects would kill the
	// command halfway through. The command timeout still bounds it.
	ctx := context.WithoutCancel(r.Context())
	err := req.perform(ctx, s.chassis)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, errUnsupportedResetType):
		s.writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, hal.ErrNotSupported):
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("ResetType %q is not supported by this configuration", req.ResetType))
	default:
		s.writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (r resetRequest) perform(ctx context.Context, chassis hal.ChassisHAL) error {
	switch r.ResetType {
	case "On":
		return chassis.SetPower(ctx, true)
	case "ForceOff":
		return chassis.SetPower(ctx, false)
	case "GracefulShutdown":
		return chassis.WarmReset(ctx)
	case "ForceRestart":
		return chassis.ColdReset(ctx)
	case "PowerCycle":
		return chassis.PowerCycle(ctx)
	default:
		return fmt.Errorf("%w %q", errUnsupportedResetType, r.ResetType)
	}
}
