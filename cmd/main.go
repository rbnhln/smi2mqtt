package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/rbnhln/smi2mqtt/internal/config"
	"github.com/rbnhln/smi2mqtt/internal/mqtt"
	"github.com/rbnhln/smi2mqtt/internal/vcs"
)

var (
	version = vcs.Version()
)

type application struct {
	config     config.Config
	logger     *slog.Logger
	mqttClient *mqtt.MqttClient
	wg         sync.WaitGroup
}

func main() {
	// init logger
	opts := &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: true,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, opts))

	displayVersion := flag.Bool("version", false, "Display version and exit")

	// load config or provide cli argument parsers
	cfg, err := config.Load("/opt/smi2mqtt/config.json")
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	flag.Parse()

	// create or update config
	err = config.Save("/opt/smi2mqtt/config.json", cfg)
	if err != nil {
		logger.Error("failed to save config", "error", err)
		os.Exit(1)
	}

	if *displayVersion {
		fmt.Printf("Version:\t%s\n", version)
		os.Exit(0)
	}

	err = cfg.Validate()
	if err != nil {
		logger.Error("invalid config", "error", err)
		os.Exit(1)
	}

	mqttClient, err := mqtt.New(cfg, logger)
	if err != nil {
		logger.Error("failed to create mqtt client", "error", err)
		os.Exit(1)
	}

	app := &application{
		config:     *cfg,
		logger:     logger,
		mqttClient: mqttClient,
	}

	// run application
	err = app.serve()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	os.Exit(0)

}
