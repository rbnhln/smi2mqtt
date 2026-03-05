package mqtt

import (
	"log/slog"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/rbnhln/smi2mqtt/internal/config"
)

const reconnectWarnThreshold = 10

type Publisher interface {
	Publish(payload string, topic string, retained bool) error
}

type MqttClient struct {
	client mqtt.Client
	logger *slog.Logger
	config *config.Config

	reconnectAttempts atomic.Uint32
	everConnected     atomic.Bool
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
	opts.SetReconnectingHandler(mqttClient.reconnectingHandler)

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
	attempts := c.reconnectAttempts.Load()

	if c.everConnected.Load() && attempts > 0 {
		c.logger.Info("Successfully reconnected to MQTT broker", "reconnect_attempts", attempts)
	} else {
		c.logger.Info("Connected to MQTT broker")
	}

	c.everConnected.Store(true)
	c.reconnectAttempts.Store(0)
}

func (c *MqttClient) connectionLostHandler(client mqtt.Client, err error) {
	c.logger.Error("Connection to MQTT broker lost", "error", err)
}

func (c *MqttClient) reconnectingHandler(client mqtt.Client, opts *mqtt.ClientOptions) {
	attempt := c.reconnectAttempts.Add(1)
	c.logger.Warn("Reconnecting to MQTT broker", "reconnect_attempt", attempt)

	if attempt >= reconnectWarnThreshold {
		c.logger.Warn("Still trying to reconnect to MQTT broker", "reconnect_attempt", attempt)
	}
}

func (c *MqttClient) Publish(message string, topic string, retain bool) error {
	token := c.client.Publish(topic, 0, retain, message)
	token.Wait()
	return token.Error()
}
