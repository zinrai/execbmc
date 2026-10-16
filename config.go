package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type config struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`

	IPMI    ipmiConfig    `json:"ipmi"`
	Redfish redfishConfig `json:"redfish"`

	BootdevPath string `json:"bootdev_path"`

	CommandTimeoutSeconds int          `json:"command_timeout_seconds"`
	Commands              commandLines `json:"commands"`
}

type ipmiConfig struct {
	Listen string `json:"listen"`
}

type redfishConfig struct {
	Listen   string `json:"listen"`
	TLS      *bool  `json:"tls"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

func (r redfishConfig) tlsEnabled() bool {
	if r.TLS == nil {
		return true
	}
	return *r.TLS
}

type commandLines struct {
	PowerStatus string `json:"power_status"`
	PowerOn     string `json:"power_on"`
	PowerOff    string `json:"power_off"`
	PowerSoft   string `json:"power_soft"`
	PowerCycle  string `json:"power_cycle"`
	Reset       string `json:"reset"`
}

func loadConfig(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

func (c *config) validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.Username == "" {
		return fmt.Errorf("username is required")
	}
	if c.Password == "" {
		return fmt.Errorf("password is required")
	}
	if c.IPMI.Listen == "" && c.Redfish.Listen == "" {
		return fmt.Errorf("at least one of ipmi.listen or redfish.listen is required")
	}
	if c.BootdevPath == "" {
		return fmt.Errorf("bootdev_path is required")
	}
	if c.Commands.PowerStatus == "" {
		return fmt.Errorf("commands.power_status is required")
	}
	if c.Commands.PowerOn == "" {
		return fmt.Errorf("commands.power_on is required")
	}
	if c.Commands.PowerOff == "" {
		return fmt.Errorf("commands.power_off is required")
	}
	if c.CommandTimeoutSeconds < 0 {
		return fmt.Errorf("command_timeout_seconds must not be negative")
	}
	return nil
}

const defaultCommandTimeout = 30 * time.Second

func (c *config) commandTimeout() time.Duration {
	if c.CommandTimeoutSeconds == 0 {
		return defaultCommandTimeout
	}
	return time.Duration(c.CommandTimeoutSeconds) * time.Second
}
