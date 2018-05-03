package xmpp

import "time"

// Config contains some configuration variables for xmpp Adapter.
type Config struct {
	HelpCommand  string        `json:"help_command" yaml:"help_command"`
	AbortCommand string        `json:"abort_command" yaml:"abort_command"`
	PingInterval time.Duration `json:"ping_interval" yaml:"ping_interval"`

	Server                       string `json:"server" yaml:"server"`
	Password                     string `json:"password" yaml:"password"`
	Jid                          string `json:"jid" yaml:"jid"`
	NoTLS                        bool   `json:"no_tls" yaml:"no_tls"`
	StartTLS                     bool   `json:"start_tls" yaml:"start_tls"`
	SkipTLSVerify                bool   `json:"skip_tls_verify" yaml:"skip_tls_verify"`
	Debug                        bool   `json:"debug" yaml:"debug"`
	Session                      bool   `json:"session" yaml:"session"`
	Status                       string `json:"status" yaml:"status"`
	StatusMessage                string `json:"status_message" yaml:"status_message"`
	Resource                     string `json:"resource" yaml:"resource"`
	InsecureAllowUnencryptedAuth bool   `json:"insecure_allow_unencrypted_auth" yaml:"insecure_allow_unencrypted_auth"`

	OAuthScope string `json:"oauth_scope" yaml:"oauth_scope"`
	OAuthToken string `json:"oauth_token" yaml:"oauth_token"`
	OAuthXmlNs string `json:"oauth_xml_ns" yaml:"oauth_xml_ns"`
}

// NewConfig returns initialized Config struct with default settings.
// Developers may override desired value by passing this to json.Unmarshal, yaml.Unmarshal or manual manipulation.
func NewConfig() *Config {
	return &Config{
		HelpCommand:  ".help",
		AbortCommand: ".abort",
		PingInterval: 30 * time.Second,

		Server:                       "",
		Password:                     "",
		Jid:                          "",
		NoTLS:                        false,
		StartTLS:                     false,
		SkipTLSVerify:                false,
		Debug:                        false,
		Session:                      false,
		Status:                       "",
		StatusMessage:                "",
		Resource:                     "",
		InsecureAllowUnencryptedAuth: false,

		OAuthScope: "",
		OAuthToken: "",
		OAuthXmlNs: "",
	}
}
