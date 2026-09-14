package config

import (
	"os"
	"testing"
)

// KAFKA_BROKERS="" is how a deployment without Kafka turns publishing off.
// env() cannot express it - it treats empty as unset and returns the fallback -
// so an empty value used to fall through to localhost:9092 and log a dropped
// event for every booking.
func TestEmptyKafkaBrokersDisablesKafka(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "")
	if got := kafkaBrokers(); len(got) != 0 {
		t.Errorf("empty KAFKA_BROKERS gave %v, want none (Kafka disabled)", got)
	}

	t.Setenv("KAFKA_BROKERS", "  ")
	if got := kafkaBrokers(); len(got) != 0 {
		t.Errorf("blank KAFKA_BROKERS gave %v, want none", got)
	}

	t.Setenv("KAFKA_BROKERS", "a:9092,b:9092")
	if got := kafkaBrokers(); len(got) != 2 {
		t.Errorf("got %v, want both brokers", got)
	}

	os.Unsetenv("KAFKA_BROKERS")
	if got := kafkaBrokers(); len(got) != 1 || got[0] != "localhost:9092" {
		t.Errorf("unset gave %v, want the localhost default", got)
	}
}
