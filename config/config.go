package config

import (
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"strings"
	"sync"
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

var (
	config *Config
	// 简单的隐藏文件记录当前版本
	versionFile = "config/.version"
)

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

	// 尝试从隐藏文件恢复上次的激活版本
	if savedVersion := loadSavedVersion(); savedVersion != "" {
		config.ActiveVersion = savedVersion
		log.Printf("成功恢复上次激活版本: %s", savedVersion)
	} else {
		log.Printf("使用配置文件默认版本: %s", config.ActiveVersion)
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
		oldVersion := config.ActiveVersion
		config.ActiveVersion = version

		// 保存到隐藏文件
		if err := saveVersion(version); err != nil {
			log.Printf("错误: 无法保存版本状态: %v", err)
			// 回滚内存状态
			config.ActiveVersion = oldVersion
			return false
		}

		log.Printf("版本切换成功: %s -> %s", oldVersion, version)
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

// 保存版本到隐藏文件 - 极简实现
func saveVersion(version string) error {
	// 确保目录存在
	err := os.MkdirAll("config", 0755)
	if err != nil {
		return err
	}
	// 直接写入版本字符串，无需解析
	return os.WriteFile(versionFile, []byte(version), 0644)
}

// 从隐藏文件加载版本 - 极简实现
func loadSavedVersion() string {
	data, err := os.ReadFile(versionFile)
	if err != nil {
		return "" // 文件不存在或读取失败，返回空字符串
	}

	version := strings.TrimSpace(string(data))
	// 简单验证
	if version == "v1" || version == "v2" {
		return version
	}

	return "" // 无效版本，返回空字符串
}
