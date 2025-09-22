package config

import (
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Backends struct {
		V1 string `yaml:"v1"`
		V2 string `yaml:"v2"`
	} `yaml:"backends"`
	ActiveVersion string `yaml:"active_version"`
	mu            sync.RWMutex
}

var config *Config

func LoadConfig(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	config = &Config{}
	err = yaml.Unmarshal(data, config)
	if err != nil {
		return err
	}

	return nil
}

func GetActiveBackend() string {
	config.mu.RLock()
	defer config.mu.RUnlock()

	if config.ActiveVersion == "v2" {
		return config.Backends.V2
	}
	return config.Backends.V1
}

func SwitchVersion(version string) bool {
	config.mu.Lock()
	defer config.mu.Unlock()

	if version == "v1" || version == "v2" {
		config.ActiveVersion = version
		return true
	}
	return false
}

func GetCurrentVersion() string {
	config.mu.RLock()
	defer config.mu.RUnlock()
	return config.ActiveVersion
}

func GetServerPort() int {
	config.mu.RLock()
	defer config.mu.RUnlock()
	return config.Server.Port
}

func GetBackends() (string, string) {
	config.mu.RLock()
	defer config.mu.RUnlock()
	return config.Backends.V1, config.Backends.V2
}
