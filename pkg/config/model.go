package config

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
)

type Config struct {
	LogLevel LogLevel `yaml:"logLevel,omitempty" json:"logLevel,omitempty"`

	Events EventsConfig `yaml:"events" json:"events"`

	Storage PostgreSQLConfig `yaml:"storage" json:"storage"`
}

type EventsConfig struct {
	// The NATS URL to connect to for consuming/producing messages.
	URL string `yaml:"url" json:"url"`

	// A unique identifier for the instance.
	InstanceID *FromEnv `yaml:"instanceID" json:"instanceID"`
}

type PostgreSQLConfig struct {
	// The PostgreSQL DSN to persist data. Example: postgres://user:secret@localhost:5432/mulval
	DSN string `yaml:"dsn" json:"dsn"`

	// An optional schema to store data into. Example: mulval
	Schema string `yaml:"schema,omitempty" json:"schema,omitempty"`

	// If turned on, run the PostgreSQL migrations before starting.
	Migrate bool `yaml:"migrate,omitempty" json:"migrate,omitempty"`

	// The minimum connections to the PostgreSQL pool.
	// Default to 4.
	MinConns int32 `yaml:"minConns,omitempty" json:"minConns,omitempty" jsonschema:"default=4"`

	// The maximum connections to the PostgreSQL pool.
	// If none set, defaults to minConns.
	MaxConns int32 `yaml:"maxConns,omitempty" json:"maxConns,omitempty"`
}

func New() *Config {
	return &Config{
		Events: EventsConfig{
			InstanceID: &FromEnv{},
		},
		Storage: PostgreSQLConfig{
			MinConns: 4,
		},
	}
}

type LogLevel string

var _ yaml.Unmarshaler = (*LogLevel)(nil)

func (ll *LogLevel) UnmarshalYAML(node *yaml.Node) error {
	if node.Value == "" {
		return nil // No value is fine, it will be defaulted
	}

	if _, err := zapcore.ParseLevel(node.Value); err != nil {
		return err
	}
	*ll = LogLevel(node.Value)
	return nil
}

func (ll LogLevel) ToZapcoreLevel() zapcore.Level {
	ls := string(ll)
	if ls == "" {
		ls = zap.InfoLevel.String() // Default the empty value to "info"
	}
	v, _ := zapcore.ParseLevel(ls)
	return v
}
