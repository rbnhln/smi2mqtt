package homeassistant

import (
	"encoding/json"
	"fmt"

	"github.com/rbnhln/smi2mqtt/internal/gpuinfo"
	"github.com/rbnhln/smi2mqtt/internal/mqtt"
)

// sensor data for HA
type SensorDescription struct {
	Name        string
	DeviceClass string
	Unit        string
	ValuePath   string
}

// HA-Device = GPU
type Device struct {
	Name         string   `json:"name"`
	Identifiers  []string `json:"identifiers"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
}

// ConfigPayload ist die Hauptstruktur für die HA-Discovery-Nachricht.
type ConfigPayload struct {
	Device            Device `json:"device"`
	Name              string `json:"name"`
	DeviceClass       string `json:"device_class,omitempty"`
	UnitOfMeasurement string `json:"unit_of_measurement,omitempty"`
	ValueTemplate     string `json:"value_template"`
	UniqueID          string `json:"unique_id"`
	StateClass        string `json:"state_class,omitempty"`
	ExpireAfter       int    `json:"expire_after"`
	EnabledByDefault  bool   `json:"enabled_by_default"`
	AvailabilityTopic string `json:"availability_topic"`
	StateTopic        string `json:"state_topic"`
}

// SensorDescriptions are package wide available for testing
var SensorDescriptions = map[string]SensorDescription{
	"pwr":     {Name: "Power Usage", DeviceClass: "power", Unit: "W", ValuePath: "dmon.pwr"},
	"gtemp":   {Name: "GPU Temp", DeviceClass: "temperature", Unit: "°C", ValuePath: "dmon.gtemp"},
	"mtemp":   {Name: "Memory Temp", DeviceClass: "temperature", Unit: "°C", ValuePath: "dmon.mtemp"},
	"sm":      {Name: "SM Util", Unit: "%", ValuePath: "dmon.sm"},
	"mem":     {Name: "Memory Util", Unit: "%", ValuePath: "dmon.mem"},
	"enc":     {Name: "Encoder Util", Unit: "%", ValuePath: "dmon.enc"},
	"dec":     {Name: "Decoder Util", Unit: "%", ValuePath: "dmon.dec"},
	"jpg":     {Name: "JPG Util", Unit: "%", ValuePath: "dmon.jpg"},
	"ofa":     {Name: "Optical Flow Util", Unit: "%", ValuePath: "dmon.ofa"},
	"mclk":    {Name: "Memory Clock", DeviceClass: "frequency", Unit: "MHz", ValuePath: "dmon.mclk"},
	"pclk":    {Name: "Processor Clock", DeviceClass: "frequency", Unit: "MHz", ValuePath: "dmon.pclk"},
	"pci":     {Name: "PCI Throughput", DeviceClass: "data_rate", Unit: "MB/s", ValuePath: "dmon.pci"},
	"rxpci":   {Name: "PCI RX", DeviceClass: "data_rate", Unit: "MB/s", ValuePath: "dmon.rxpci"},
	"txpci":   {Name: "PCI TX", DeviceClass: "data_rate", Unit: "MB/s", ValuePath: "dmon.txpci"},
	"utilgpu": {Name: "GPU Utilization", Unit: "%", ValuePath: "query.utilgpu"},
	"memused": {Name: "Memory Used", DeviceClass: "data_size", Unit: "MiB", ValuePath: "query.memused"},
	"memfree": {Name: "Memory Free", DeviceClass: "data_size", Unit: "MiB", ValuePath: "query.memfree"},
	"drivver": {Name: "Driver Version", ValuePath: "query.drivver"},
	"fanspe":  {Name: "Fan Speed", Unit: "%", ValuePath: "query.fanspe"},
	"pstat":   {Name: "Power State", ValuePath: "query.pstat"},
}

func PublishConfigs(client mqtt.Publisher, gpus []gpuinfo.GPU, baseTopic string) error {
	availabilityTopic := fmt.Sprintf("%s/availability", baseTopic)

	for _, gpu := range gpus {
		device := Device{
			Name:         gpu.Name,
			Identifiers:  []string{gpu.Uuid},
			Manufacturer: "NVIDIA",
			Model:        gpu.Name,
		}
		stateTopic := fmt.Sprintf("%s/%s/state", baseTopic, gpu.Uuid)

		for key, desc := range SensorDescriptions {
			configTopic := fmt.Sprintf("homeassistant/sensor/%s_%s/config", gpu.Uuid, key)

			payload := ConfigPayload{
				Device:            device,
				Name:              desc.Name,
				DeviceClass:       desc.DeviceClass,
				UnitOfMeasurement: desc.Unit,
				ValueTemplate:     fmt.Sprintf("{{ value_json.%s }}", desc.ValuePath),
				UniqueID:          fmt.Sprintf("%s_%s", gpu.Uuid, key),
				StateClass:        "measurement",
				ExpireAfter:       60,
				EnabledByDefault:  true,
				AvailabilityTopic: availabilityTopic,
				StateTopic:        stateTopic,
			}

			if desc.Unit == "" {
				payload.StateClass = ""
			}

			payloadBytes, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("failed to marshal config for %s: %w", payload.UniqueID, err)
			}

			if err := client.Publish(string(payloadBytes), configTopic, true); err != nil {
				return fmt.Errorf("failed to publish config for %s: %w", payload.UniqueID, err)
			}
		}
	}

	if err := client.Publish("online", availabilityTopic, true); err != nil {
		return fmt.Errorf("failed to publish availability: %w", err)
	}

	return nil
}
