package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rbnhln/smi2mqtt/internal/gpuinfo"
	"github.com/rbnhln/smi2mqtt/internal/homeassistant"
)

type GpuPublishedState struct {
	State     gpuinfo.GpuState
	Timestamp time.Time
}

func (app *application) serve() error {
	// MQTT Connect
	err := app.mqttClient.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to mqtt broker: %w", err)
	}
	defer app.mqttClient.Disconnect()
	app.logger.Info("successfully connected to mqtt broker")

	// Check GPUs
	listGpus, err := gpuinfo.GetGpuInfo()
	if err != nil {
		return fmt.Errorf("failed to find nvidia gpus: %w", err)
	}
	if len(listGpus) == 0 {
		return fmt.Errorf("found 0 nvidia gpus")
	}

	// MQTT HA Auto-Discovery
	if app.config.HA {
		app.logger.Info("publishing home assistant auto-discovery configs")
		err := homeassistant.PublishConfigs(app.mqttClient, listGpus, app.config.Topic)
		if err != nil {
			app.logger.Warn("failed to publish HA discovery configs", "error", err)
		}
	}

	// Create context for clean shutdown of goroutines
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mergedStateChan := make(chan gpuinfo.GpuState)
	var forwarderWg sync.WaitGroup

	for _, gpu := range listGpus {
		currentGpu := gpu

		// combined monitor for both
		stateChan, err := gpuinfo.CombinedMonitor(ctx, app.logger, currentGpu)
		if err != nil {
			app.logger.Error("failed to start combined monitor for gpu", "gpu_id", currentGpu.Index, "error", err)
			continue
		}

		forwarderWg.Add(1)
		app.background(func() {
			defer forwarderWg.Done()
			for state := range stateChan {
				select {
				case mergedStateChan <- state:
				case <-ctx.Done():
					app.logger.Debug("forwarder context cancelled", "gpu_uuid", currentGpu.Uuid)
					return
				}
			}
			app.logger.Debug("forwarder finished", "gpu_uuid", currentGpu.Uuid)
		})
	}

	go func() {
		forwarderWg.Wait()
		close(mergedStateChan)
		app.logger.Debug("all forwarders finished, closed merged channel")
	}()

	app.background(func() {
		app.logger.Info("starting main metrics consumer")
		lastPublished := make(map[string]GpuPublishedState)
		forcePublishInterval := 30 * time.Second

		for state := range mergedStateChan {
			uuid := state.Gpu.Uuid
			lastState, found := lastPublished[uuid]
			if !found || state != lastState.State || time.Since(lastState.Timestamp) > forcePublishInterval {
				payload, err := json.Marshal(state)
				if err != nil {
					app.logger.Error("failed to marshal metrics", "gpu_uuid", state.Gpu.Uuid, "error", err)
					continue
				}

				topic := fmt.Sprintf("%s/%s/state", app.config.Topic, state.Gpu.Uuid)
				if err := app.mqttClient.Publish(string(payload), topic, false); err != nil {
					app.logger.Error("failed to publish metrics", "gpu_uuid", state.Gpu.Uuid, "error", err)
				}

				lastPublished[uuid] = GpuPublishedState{
					State:     state,
					Timestamp: time.Now(),
				}
			}
		}
		app.logger.Info("main metrics consumer stopped")
	})

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	s := <-quit
	app.logger.Info("caught signal", "signal", s.String())
	app.logger.Info("shutting down")

	cancel()

	// Gracefull shutdown with timeout
	done := make(chan struct{})
	go func() {
		app.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		app.logger.Info("all goroutines finished gracefully")
	case <-time.After(10 * time.Second):
		app.logger.Warn("shutdown timeout, forcing exit")
	}
	return nil
}
