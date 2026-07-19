package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/bougou/go-ipmi/pkg/types"
)

// Boot device targets written to the bootdev file. The consumer (the script
// that builds the machine's boot arguments) reads the file at machine start
// and, when persistent is false, resets the file to none after one boot.
const (
	targetNone  = "none"
	targetPXE   = "pxe"
	targetDisk  = "disk"
	targetCDROM = "cdrom"
)

// bootdevFile is the JSON document written to bootdev_path.
type bootdevFile struct {
	Target     string `json:"target"`
	Persistent bool   `json:"persistent"`
}

// bootdevStore holds the current boot flags in memory and mirrors the boot
// device selection to a file. execbmc only writes the file; consuming the
// one-shot flag is the reader's responsibility.
type bootdevStore struct {
	mu    sync.Mutex
	path  string
	flags types.BootOptionParam_BootFlags
	ack   types.BootOptionParam_BootInfoAcknowledge
}

func newBootdevStore(path string) *bootdevStore {
	return &bootdevStore{path: path}
}

// selectorToTarget maps the IPMI boot device selector to a file target.
func selectorToTarget(sel types.BootDeviceSelector) (string, error) {
	switch sel {
	case types.BootDeviceSelectorNoOverride:
		return targetNone, nil
	case types.BootDeviceSelectorForcePXE:
		return targetPXE, nil
	case types.BootDeviceSelectorForceHardDrive:
		return targetDisk, nil
	case types.BootDeviceSelectorForceCDROM:
		return targetCDROM, nil
	default:
		return "", fmt.Errorf("boot device selector %#02x is not supported", uint8(sel))
	}
}

// targetToSelector is the inverse of selectorToTarget.
func targetToSelector(target string) (types.BootDeviceSelector, error) {
	switch target {
	case targetNone:
		return types.BootDeviceSelectorNoOverride, nil
	case targetPXE:
		return types.BootDeviceSelectorForcePXE, nil
	case targetDisk:
		return types.BootDeviceSelectorForceHardDrive, nil
	case targetCDROM:
		return types.BootDeviceSelectorForceCDROM, nil
	default:
		return 0, fmt.Errorf("boot target %q is not supported", target)
	}
}

// Set validates the flags, stores them, and writes the bootdev file.
func (s *bootdevStore) Set(flags *types.BootOptionParam_BootFlags) error {
	target, err := selectorToTarget(flags.BootDeviceSelector)
	if err != nil {
		return err
	}
	if !flags.BootFlagsValid {
		target = targetNone
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.write(bootdevFile{Target: target, Persistent: flags.Persist}); err != nil {
		return err
	}
	s.flags = *flags
	return nil
}

// Get returns a copy of the stored boot flags.
func (s *bootdevStore) Get() *types.BootOptionParam_BootFlags {
	s.mu.Lock()
	defer s.mu.Unlock()
	flags := s.flags
	return &flags
}

// Target returns the stored boot selection as a file target.
func (s *bootdevStore) Target() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.flags.BootFlagsValid {
		return targetNone, false
	}
	target, err := selectorToTarget(s.flags.BootDeviceSelector)
	if err != nil {
		return targetNone, false
	}
	return target, s.flags.Persist
}

// SetAck stores the boot initiator acknowledge data.
func (s *bootdevStore) SetAck(ack *types.BootOptionParam_BootInfoAcknowledge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ack = *ack
}

// GetAck returns a copy of the stored acknowledge data.
func (s *bootdevStore) GetAck() *types.BootOptionParam_BootInfoAcknowledge {
	s.mu.Lock()
	defer s.mu.Unlock()
	ack := s.ack
	return &ack
}

// write persists the bootdev file atomically: write to a temp file in the
// same directory, then rename over the destination.
func (s *bootdevStore) write(f bootdevFile) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".bootdev-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
