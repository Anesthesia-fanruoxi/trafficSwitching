package config

import (
	"gopkg.in/yaml.v3"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Backends struct {
		V1 string `yaml:"v1"`
		V2 string `yaml:"v2"`
	} `yaml:"backends"`
	HealthAPI     string `yaml:"health_api"`
	ActiveVersion string `yaml:"active_version"`
	Report        struct {
		Enable   bool   `yaml:"enable"`
		Project  string `yaml:"project"`
		Source   string `yaml:"source"`
		Service  string `yaml:"service"`
		Interval string `yaml:"interval"`
		URL      string `yaml:"url"`
		Salt     string `yaml:"salt"`
	} `yaml:"report"`
	Proxy struct {
		ForceLowerHeaders []string `yaml:"force_lower_headers"`
	} `yaml:"proxy"`

	// 内部预计算高速缓存变量（不出具于 yaml）
	parsedLowerHeaders map[string]string

	mu sync.RWMutex
}

var (
	config      *Config
	versionFile = "config/.version"
)

func LoadConfig(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	config = &Config{}
	if err = yaml.Unmarshal(data, config); err != nil {
		return err
	}

	config.parsedLowerHeaders = buildLowerHeadersMap(config.Proxy.ForceLowerHeaders)

	if savedVersion := loadSavedVersion(); savedVersion != "" {
		config.ActiveVersion = savedVersion
	}
	return nil
}

// buildLowerHeadersMap 辅助函数：预转换生成字典
func buildLowerHeadersMap(headers []string) map[string]string {
	m := make(map[string]string, len(headers))
	for _, h := range headers {
		// 预先算好 O(1) 字典 [大写原始标准格式] -> [绝对强制小写格式]
		m[http.CanonicalHeaderKey(h)] = strings.ToLower(h)
	}
	return m
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
			log.Printf("ERROR: 无法保存版本状态: %v", err)
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

// GetHealthAPI 获取健康检查端点，如果未配置则返回默认值
func GetHealthAPI() string {
	config.mu.RLock()
	defer config.mu.RUnlock()
	if config.HealthAPI == "" {
		return "/actuator/health" // 默认值
	}
	return config.HealthAPI
}

// GetReportConfig 获取上报配置
func GetReportConfig() (enable bool, project, source, service, interval, url, salt string) {
	config.mu.RLock()
	defer config.mu.RUnlock()
	return config.Report.Enable, config.Report.Project, config.Report.Source, config.Report.Service,
		config.Report.Interval, config.Report.URL, config.Report.Salt
}

func saveVersion(version string) error {
	if err := os.MkdirAll("config", 0755); err != nil {
		return err
	}
	return os.WriteFile(versionFile, []byte(version), 0644)
}

func loadSavedVersion() string {
	data, err := os.ReadFile(versionFile)
	if err != nil {
		return ""
	}
	version := strings.TrimSpace(string(data))
	if version == "v1" || version == "v2" {
		return version
	}
	return ""
}

// GetParsedLowerHeadersMap 获取已预计算好的 Header 映射字典
func GetParsedLowerHeadersMap() map[string]string {
	config.mu.RLock()
	defer config.mu.RUnlock()
	// 返回内部 Map，由于热重载时我们是生成一个全新 Map 并整体替换指针，这里并发读极其安全
	return config.parsedLowerHeaders
}

// WatchConfig 监听配置文件变化并热更新
func WatchConfig(filename string) {
	go func() {
		var lastModTime time.Time
		if stat, err := os.Stat(filename); err == nil {
			lastModTime = stat.ModTime()
		}

		// 使用轮询机制监测文件更新，避免引入第三方监控库的兼容问题
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			stat, err := os.Stat(filename)
			if err != nil {
				continue
			}
			if stat.ModTime().After(lastModTime) {
				lastModTime = stat.ModTime()
				log.Println("配置: 检测到配置文件变更，正在热更新...")

				data, err := os.ReadFile(filename)
				if err != nil {
					log.Printf("配置: 热更新读取失败: %v", err)
					continue
				}

				newConfig := &Config{}
				if err := yaml.Unmarshal(data, newConfig); err != nil {
					log.Printf("配置: 热更新解析YAML失败: %v", err)
					continue
				}

				// 提前预计算新配置的优化字典
				newConfig.parsedLowerHeaders = buildLowerHeadersMap(newConfig.Proxy.ForceLowerHeaders)

				// 在原指针上更新，避免替换指针导致的 sync.RWMutex 被更换从而触发 Unlock of unlocked RWMutex
				config.mu.Lock()
				config.Server = newConfig.Server
				config.Backends = newConfig.Backends
				config.HealthAPI = newConfig.HealthAPI
				config.Report = newConfig.Report
				config.Proxy = newConfig.Proxy
				config.parsedLowerHeaders = newConfig.parsedLowerHeaders
				// 注意：故意不覆盖 ActiveVersion，以维持当前内存状态
				config.mu.Unlock()

				log.Println("配置: 配置文件热更新成功")
			}
		}
	}()
}
