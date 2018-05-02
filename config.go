package xmpp

import "time"

// Config contains some configuration variables for xmpp Adapter.
type Config struct {
	Server           string        `json:"server" yaml:"server"`
	Password         string        `json:"password" yaml:"password"`
	Jid              string        `json:"jid" yaml:"jid"`
	NoTLS            bool          `json:"notls" yaml:"notls"`
	StartTLS         bool          `json:"starttls" yaml:"starttls"`
	SkipTLSVerify    bool          `json:"skiptlsverify" yaml:"skiptlsverify"`
	Debug            bool          `json:"debug" yaml:"debug"`
	HelpCommand      string        `json:"help_command" yaml:"help_command"`
	AbortCommand     string        `json:"abort_command" yaml:"abort_command"`
	PingInterval     time.Duration `json:"ping_interval" yaml:"ping_interval"`
}

// NewConfig returns initialized Config struct with default settings.
func NewConfig() *Config {
	return &Config{
		Server:           "127.0.0.1",
		Password:         "pw",
		NoTLS:            true,
		StartTLS:         false,
		SkipTLSVerify:    true,
		Debug:            true,
		HelpCommand:      ".help",
		AbortCommand:     ".abort",
		PingInterval:     30 * time.Second,
	}
}

