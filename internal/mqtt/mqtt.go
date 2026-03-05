package mqtt

import (
	"log/slog"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/rbnhln/smi2mqtt/internal/config"
)

const reconnectWarnThreshold = 10

// Create interface to enable tests
type Publisher interface {
	Publish(payload string, topic string, retained bool) error
}

type MqttClient struct {
	client         mqtt.Client
	logger         *slog.Logger
	config         *config.Config
	reconnectCount atomic.Uint32
}

func New(cfg *config.Config, logger *slog.Logger) (*MqttClient, error) {
	mqttClient := &MqttClient{
		logger: logger,
		config: cfg,
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(cfg.Broker)
	opts.SetClientID(cfg.ClientID)
	opts.SetUsername(cfg.MqttUsername)
	opts.SetPassword(cfg.MqttPassword)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectTimeout(5 * time.Second)
	opts.SetConnectRetryInterval(2 * time.Second)
	opts.OnConnect = mqttClient.connectHandler
	opts.OnConnectionLost = mqttClient.connectionLostHandler

	mqttClient.client = mqtt.NewClient(opts)

	return mqttClient, nil
}

func (c *MqttClient) Connect() error {
	if token := c.client.Connect(); token.Wait() && token.Error() != nil {
		return token.Error()
	}
	return nil
}

func (c *MqttClient) Disconnect() {
	c.client.Disconnect(250)
}

func (c *MqttClient) connectHandler(client mqtt.Client) {
	count := c.reconnectCount.Load()
	c.logger.Info("Connected to MQTT Broker")
	if count > 0 {
		c.logger.Info("Successfully reconnected to MQTT broker", "reconnect_count", count)
		c.reconnectCount.Store(0)
	}
}

func (c *MqttClient) connectionLostHandler(client mqtt.Client, err error) {
	count := c.reconnectCount.Add(1)
	c.logger.Error("Connection to MQTT broker lost", "error", err, "reconnect_attempt", count)
	
	// Log when we've been trying to reconnect for a while
	if count > reconnectWarnThreshold {
		c.logger.Warn("Still trying to reconnect to MQTT broker", "reconnect_attempt", count)
	}
	
	// Reset count after successful reconnection (handled in connectHandler)
	// The Paho MQTT client handles the actual reconnection with SetAutoReconnect(true)
	// We just need to track and log the attempts
}

func (c *MqttClient) Publish(message string, topic string, retain bool) error {
	token := c.client.Publish(topic, 0, retain, message)
	token.Wait()
	return token.Error()
}
