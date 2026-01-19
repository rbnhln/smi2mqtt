package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
)

type Config struct {
	Broker         string `json:"broker"`
	ClientID       string `json:"client_id"`
	Topic          string `json:"topic"`
	MqttUsername   string `json:"mqtt_username"`
	MqttPassword   string `json:"mqtt_password"`
	HA             bool   `json:"ha"`
	UpdateInterval int    `json:"update_interval"`
}

// Load config
func Load(path string) (*Config, error) {
	cfg := &Config{}

	// set default values for HA and mqtt Topic
	cfg.HA = true
	cfg.Topic = "smi2mqtt"
	cfg.UpdateInterval = 1

	// Load values from config file, if present
	file, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(file, cfg)
	}

	// create cli flags, and overwrite if provided
	flag.StringVar(&cfg.Broker, "broker", cfg.Broker, "Broker address (e.g., tcp://127.0.0.1:1883)")
	flag.StringVar(&cfg.Topic, "topic", cfg.Topic, "mqtt topic for smi2mqtt")
	flag.StringVar(&cfg.MqttUsername, "username", cfg.MqttUsername, "username for mqtt server")
	flag.StringVar(&cfg.MqttPassword, "password", cfg.MqttPassword, "password for mqtt server")
	flag.BoolVar(&cfg.HA, "ha", cfg.HA, "Use Home Assistant auto-discovery")
	flag.IntVar(&cfg.UpdateInterval, "interval", cfg.UpdateInterval, "Update interval in seconds (default: 1)")

	return cfg, nil
}

// Save provided values to config
func Save(path string, cfg *Config) error {
	// creates client-id if necessary
	if cfg.ClientID == "" {
		cfg.ClientID = uuid.NewString()
	}

	// write values to file
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func (c *Config) Validate() error {
	if c.Broker == "" {
		return fmt.Errorf("broker address is required")
	}
	if c.Topic == "" {
		return fmt.Errorf("topic is required")
	}
	if c.UpdateInterval < 1 {
		return fmt.Errorf("update interval must be at least 1 second")
	}
	return nil
}
