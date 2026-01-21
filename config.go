package main

import (
	"fmt"
	"sync"

	"os"

	yaml "gopkg.in/yaml.v2"
)

type Config struct {
	Hosts      map[string]HostConfig `yaml:"hosts"`
	Groups     map[string]HostConfig `yaml:"groups"`
	Collectors []string              `yaml:"collectors"`
	Loglevel   string                `yaml:"loglevel"`
}

type SafeConfig struct {
	sync.RWMutex
	Config *Config
}

type HostConfig struct {
	Username   string   `yaml:"username"`
	Password   string   `yaml:"password"`
	Collectors []string `yaml:"collectors"`
}

// Read exporter config from file
func NewConfigFromFile(configFile string) (*Config, error) {
	var config = &Config{}
	yamlFile, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(yamlFile, config); err != nil {
		return nil, err
	}

	return config, nil
}

func (sc *SafeConfig) ReloadConfig(configFile string) error {
	var c, err = NewConfigFromFile(configFile)
	if err != nil {
		return err
	}

	sc.Lock()
	sc.Config = c
	sc.Unlock()

	return nil
}

func (sc *SafeConfig) HostConfigForTarget(target string) (*HostConfig, error) {
	sc.Lock()
	defer sc.Unlock()

	var hostConfig HostConfig
	var found bool

	if hc, ok := sc.Config.Hosts[target]; ok {
		hostConfig = hc
		found = true
	} else if hc, ok := sc.Config.Hosts["default"]; ok {
		hostConfig = hc
		found = true
	}

	if !found {
		return &HostConfig{}, fmt.Errorf("no credentials found for target %s", target)
	}

	// Use per-host collectors if set, otherwise fall back to global collectors
	collectors := hostConfig.Collectors
	if len(collectors) == 0 {
		collectors = sc.Config.Collectors
	}

	return &HostConfig{
		Username:   hostConfig.Username,
		Password:   hostConfig.Password,
		Collectors: collectors,
	}, nil
}

// HostConfigForGroup checks the configuration for a matching group config and returns the configured HostConfig for
// that matched group.
func (sc *SafeConfig) HostConfigForGroup(group string) (*HostConfig, error) {
	sc.Lock()
	defer sc.Unlock()
	if hostConfig, ok := sc.Config.Groups[group]; ok {
		// Use per-group collectors if set, otherwise fall back to global collectors
		collectors := hostConfig.Collectors
		if len(collectors) == 0 {
			collectors = sc.Config.Collectors
		}
		return &HostConfig{
			Username:   hostConfig.Username,
			Password:   hostConfig.Password,
			Collectors: collectors,
		}, nil
	}
	return &HostConfig{}, fmt.Errorf("no credentials found for group %s", group)
}

func (sc *SafeConfig) AppLogLevel() string {
	sc.Lock()
	defer sc.Unlock()
	logLevel := sc.Config.Loglevel
	if logLevel != "" {
		return logLevel
	}
	return "info"
}
