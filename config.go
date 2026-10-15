package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Config struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"`

	IPMI    IPMIConfig    `json:"ipmi"`
	Redfish RedfishConfig `json:"redfish"`

	BootdevPath string `json:"bootdev_path"`

	CommandTimeoutSeconds int      `json:"command_timeout_seconds"`
	Commands              Commands `json:"commands"`
}

type IPMIConfig struct {
	Listen string `json:"listen"`
}

type RedfishConfig struct {
	Listen   string `json:"listen"`
	TLS      *bool  `json:"tls"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

func (r RedfishConfig) TLSEnabled() bool {
	if r.TLS == nil {
		return true
	}
	return *r.TLS
}

type Commands struct {
	PowerStatus string `json:"power_status"`
	PowerOn     string `json:"power_on"`
	PowerOff    string `json:"power_off"`
	PowerSoft   string `json:"power_soft"`
	PowerCycle  string `json:"power_cycle"`
	Reset       string `json:"reset"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
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

func (c *Config) CommandTimeout() time.Duration {
	if c.CommandTimeoutSeconds == 0 {
		return 30 * time.Second
	}
	return time.Duration(c.CommandTimeoutSeconds) * time.Second
}
