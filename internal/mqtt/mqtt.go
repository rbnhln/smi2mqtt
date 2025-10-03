package mqtt

import (
	"log/slog"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/rbnhln/smi2mqtt/internal/config"
)

// Create interface to enable tests
type Publisher interface {
	Publish(payload string, topic string, retained bool) error
}

type MqttClient struct {
	client mqtt.Client
	logger *slog.Logger
	config *config.Config
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
	c.logger.Info("Connected to MQTT Broker")
}

func (c *MqttClient) connectionLostHandler(client mqtt.Client, err error) {
	c.logger.Error("Connection to MQTT broker lost", "error", err)
}

func (c *MqttClient) Publish(message string, topic string, retain bool) error {
	token := c.client.Publish(topic, 0, retain, message)
	token.Wait()
	return token.Error()
}
