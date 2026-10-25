package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config 是 DevOS 的持久化配置。
type Config struct {
	Port           int  `json:"port"`
	OpenBrowser    bool `json:"openBrowser"`
	StartMinimized bool `json:"startMinimized"`
	MaxPortTries   int  `json:"maxPortTries"`
}

func Default() Config {
	return Config{
		Port:           7788,
		OpenBrowser:    true,
		StartMinimized: false,
		MaxPortTries:   20,
	}
}

// Load 从 dir/config.json 读取配置，不存在时写入默认配置。
func Load(dir string) (Config, error) {
	cfg := Default()
	path := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if werr := writeDefault(path, cfg); werr != nil {
				return cfg, werr
			}
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), err
	}
	if cfg.Port == 0 {
		cfg.Port = 7788
	}
	if cfg.MaxPortTries <= 0 {
		cfg.MaxPortTries = 20
	}
	return cfg, nil
}

func writeDefault(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
