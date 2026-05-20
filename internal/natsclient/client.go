package natsclient

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"test-automation-agents/pkg/models"

	"github.com/nats-io/nats.go"
)

// Client обертка для NATS соединения
type Client struct {
	Conn *nats.Conn
	JS   nats.JetStreamContext
}

// NewClient создает новое подключение к NATS
func NewClient(natsURL string) (*Client, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	return &Client{Conn: nc, JS: js}, nil
}

// Close закрывает соединение
func (c *Client) Close() {
	c.Conn.Close()
}

// PublishResponse отправляет ответ: через request-reply, если задан msg.Reply, иначе в subject.
func (c *Client) PublishResponse(msg *nats.Msg, subject string, data []byte) error {
	if msg.Reply != "" {
		if err := msg.Respond(data); err != nil {
			return fmt.Errorf("failed to respond on %s: %w", msg.Reply, err)
		}
		log.Printf("Response sent to reply inbox %s", msg.Reply)
		return nil
	}
	if err := c.Conn.Publish(subject, data); err != nil {
		return fmt.Errorf("failed to publish to %s: %w", subject, err)
	}
	log.Printf("Response published to %s", subject)
	return nil
}

// PublishMessage публикует сообщение в указанный subject
func (c *Client) PublishMessage(subject string, msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	if err := c.Conn.Publish(subject, data); err != nil {
		return fmt.Errorf("failed to publish to %s: %w", subject, err)
	}

	log.Printf("Message published to %s", subject)
	return nil
}

// Subscribe создает подписку на subject с обработчиком
func (c *Client) Subscribe(subject string, handler func(*nats.Msg)) (*nats.Subscription, error) {
	sub, err := c.Conn.Subscribe(subject, handler)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to %s: %w", subject, err)
	}
	log.Printf("Subscribed to %s", subject)
	return sub, nil
}

// Request отправляет запрос и ожидает ответа
func (c *Client) Request(subject string, req interface{}, timeout time.Duration) (*nats.Msg, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	msg, err := c.Conn.Request(subject, data, timeout)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", subject, err)
	}
	return msg, nil
}

// CreateStream создает JetStream stream
func (c *Client) CreateStream(streamName string, subjects []string) error {
	stream, err := c.JS.StreamInfo(streamName)
	if err == nil && stream != nil {
		log.Printf("Stream %s already exists", streamName)
		return nil
	}

	_, err = c.JS.AddStream(&nats.StreamConfig{
		Name:     streamName,
		Subjects: subjects,
		MaxAge:   24 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("failed to create stream %s: %w", streamName, err)
	}
	log.Printf("Stream %s created", streamName)
	return nil
}

// Helper для создания базового сообщения
func CreateBaseMessage(msgType, source, target string) models.BaseMessage {
	return models.BaseMessage{
		ID:        generateID(),
		Type:      msgType,
		Timestamp: time.Now(),
		Source:    source,
		Target:    target,
	}
}

func generateID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}
