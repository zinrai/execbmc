package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/bougou/go-ipmi/pkg/types"
)

const (
	targetNone  = "none"
	targetPXE   = "pxe"
	targetDisk  = "disk"
	targetCDROM = "cdrom"
)

type bootdevFile struct {
	Target     string `json:"target"`
	Persistent bool   `json:"persistent"`
}

type bootdevStore struct {
	mu    sync.Mutex
	path  string
	flags types.BootOptionParam_BootFlags
	ack   types.BootOptionParam_BootInfoAcknowledge
}

func newBootdevStore(path string) *bootdevStore {
	return &bootdevStore{path: path}
}

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

func (s *bootdevStore) Set(flags *types.BootOptionParam_BootFlags) (bootdevFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set(flags)
}

func (s *bootdevStore) Get() (*types.BootOptionParam_BootFlags, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get()
}

func (s *bootdevStore) Update(change func(types.BootOptionParam_BootFlags) (types.BootOptionParam_BootFlags, error)) (bootdevFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.get()
	if err != nil {
		return bootdevFile{}, err
	}
	flags, err := change(*current)
	if err != nil {
		return bootdevFile{}, err
	}
	return s.set(&flags)
}

func (s *bootdevStore) set(flags *types.BootOptionParam_BootFlags) (bootdevFile, error) {
	target, err := selectorToTarget(flags.BootDeviceSelector)
	if err != nil {
		return bootdevFile{}, err
	}
	if !flags.BootFlagsValid {
		target = targetNone
	}
	f := bootdevFile{Target: target, Persistent: flags.Persist}
	if err := s.write(f); err != nil {
		return bootdevFile{}, err
	}
	s.flags = *flags
	return f, nil
}

func (s *bootdevStore) get() (*types.BootOptionParam_BootFlags, error) {
	// Not from memory alone: the file's reader, not execbmc, resets a
	// one-shot override after the boot.
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	flags := s.flags
	flags.Persist = f.Persistent
	// Not the selector from the file: it holds none while the override is
	// disabled, and the target stored in memory stays visible.
	if f.Target == targetNone {
		flags.BootFlagsValid = false
		return &flags, nil
	}
	sel, err := targetToSelector(f.Target)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	flags.BootFlagsValid = true
	flags.BootDeviceSelector = sel
	return &flags, nil
}

func (s *bootdevStore) SetAck(ack *types.BootOptionParam_BootInfoAcknowledge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ack = *ack
}

func (s *bootdevStore) GetAck() *types.BootOptionParam_BootInfoAcknowledge {
	s.mu.Lock()
	defer s.mu.Unlock()
	ack := s.ack
	return &ack
}

func (s *bootdevStore) read() (bootdevFile, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return bootdevFile{Target: targetNone}, nil
	}
	if err != nil {
		return bootdevFile{}, err
	}
	var f bootdevFile
	if err := json.Unmarshal(data, &f); err != nil {
		return bootdevFile{}, fmt.Errorf("parse %s: %w", s.path, err)
	}
	return f, nil
}

func (s *bootdevStore) write(f bootdevFile) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// Not written in place: the reader could see a partial document.
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
