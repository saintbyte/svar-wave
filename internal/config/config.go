package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type HTTP struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	BodyLimitMB int    `yaml:"body_limit_mb"`
}

type Storage struct {
	Dir string `yaml:"dir"`
}

type Player struct {
	SampleRate int `yaml:"sample_rate"`
	BufferMS   int `yaml:"buffer_ms"`
	Quality    int `yaml:"resample_quality"`
}

type Daemon struct {
	PidFile string `yaml:"pid_file"`
	LogFile string `yaml:"log_file"`
	Umask   int    `yaml:"umask"`
}

type Upload struct {
	Autoplay bool `yaml:"autoplay"`
}

type Log struct {
	Level string `yaml:"level"`
}

type Config struct {
	HTTP    HTTP    `yaml:"http"`
	Storage Storage `yaml:"storage"`
	Player  Player  `yaml:"player"`
	Daemon  Daemon  `yaml:"daemon"`
	Upload  Upload  `yaml:"upload"`
	Log     Log     `yaml:"log"`
}

func Default() *Config {
	c := &Config{}
	c.HTTP.Host = "0.0.0.0"
	c.HTTP.Port = 8080
	c.HTTP.BodyLimitMB = 100
	c.Storage.Dir = "./uploads"
	c.Player.SampleRate = 44100
	c.Player.BufferMS = 200
	c.Player.Quality = 3
	c.Daemon.PidFile = "/tmp/svar-wave.pid"
	c.Daemon.LogFile = ""
	c.Daemon.Umask = 027
	c.Upload.Autoplay = true
	c.Log.Level = "info"
	return c
}

func Load(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.HTTP.Port <= 0 || c.HTTP.Port > 65535 {
		return fmt.Errorf("http.port must be in 1..65535")
	}
	if c.HTTP.BodyLimitMB <= 0 {
		c.HTTP.BodyLimitMB = 100
	}
	if c.Storage.Dir == "" {
		c.Storage.Dir = "./uploads"
	}
	c.Storage.Dir = filepath.Clean(c.Storage.Dir)
	if c.Player.SampleRate < 8000 || c.Player.SampleRate > 384000 {
		return fmt.Errorf("player.sample_rate must be in 8000..384000")
	}
	if c.Player.BufferMS <= 0 {
		c.Player.BufferMS = 200
	}
	if c.Player.Quality < 1 || c.Player.Quality > 4 {
		c.Player.Quality = 3
	}
	if c.Daemon.PidFile == "" {
		c.Daemon.PidFile = "/tmp/svar-wave.pid"
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	return nil
}
