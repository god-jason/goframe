package mqtt

import (
	"context"
	"errors"
	"testing"

	"github.com/god-jason/goframe/logger"
)

func testOptions() Options {
	return Options{
		Logger:  logger.NewNop(),
		Brokers: []string{"127.0.0.1:1883"},
	}
}

func TestNewValidationAndDefaults(t *testing.T) {
	if _, err := New(Options{}); !errors.Is(err, ErrNoBrokers) {
		t.Fatalf("New without brokers = %v, want ErrNoBrokers", err)
	}

	client, err := New(testOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stats := client.Stats()
	if stats.ClientID == "" {
		t.Fatal("expected generated client id")
	}
	if len(stats.Brokers) != 1 || stats.Connected {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestPublishSubscribeRequireConnection(t *testing.T) {
	client, err := New(testOptions())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	if err := client.Publish(ctx, "devices/1/up", 0, false, []byte("x")); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Publish = %v, want ErrNotConnected", err)
	}
	if err := client.Subscribe(ctx, "devices/+/up", 0, func(string, []byte) {}); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Subscribe = %v, want ErrNotConnected", err)
	}
	if err := client.Unsubscribe(ctx, "devices/+/up"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Unsubscribe = %v, want ErrNotConnected", err)
	}
	if client.IsConnected() {
		t.Fatal("client must not be connected")
	}
}

func TestTopicValidation(t *testing.T) {
	publishCases := []struct {
		topic string
		ok    bool
	}{
		{"devices/1/up", true},
		{"", false},
		{"devices/+/up", false},
		{"devices/#", false},
		{"devices/1/up\x00", false},
	}
	for _, tc := range publishCases {
		err := validatePublishTopic(tc.topic)
		if tc.ok && err != nil {
			t.Fatalf("publish topic %q: %v", tc.topic, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidTopic) {
			t.Fatalf("publish topic %q = %v, want ErrInvalidTopic", tc.topic, err)
		}
	}

	subscribeCases := []struct {
		topic string
		ok    bool
	}{
		{"devices/+/up", true},
		{"devices/#", true},
		{"#", true},
		{"devices/#/up", false},
		{"devices/te+st/up", false},
		{"", false},
	}
	for _, tc := range subscribeCases {
		err := validateSubscribeTopic(tc.topic)
		if tc.ok && err != nil {
			t.Fatalf("subscribe topic %q: %v", tc.topic, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidTopic) {
			t.Fatalf("subscribe topic %q = %v, want ErrInvalidTopic", tc.topic, err)
		}
	}
}

func TestSubscribeValidation(t *testing.T) {
	client, _ := New(testOptions())
	if err := client.Subscribe(context.Background(), "a/b", 0, nil); err == nil {
		t.Fatal("expected nil handler error")
	}
	if err := client.Subscribe(context.Background(), "a/#/b", 0, func(string, []byte) {}); !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("invalid filter = %v", err)
	}
}

func TestNormalizeBroker(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:1883":          "tcp://127.0.0.1:1883",
		"tcp://127.0.0.1:1883":    "tcp://127.0.0.1:1883",
		"ssl://mqtt.example:8883": "ssl://mqtt.example:8883",
		"  ":                      "",
	}
	for input, want := range cases {
		if got := normalizeBroker(input); got != want {
			t.Fatalf("normalizeBroker(%q) = %q, want %q", input, got, want)
		}
	}
}
