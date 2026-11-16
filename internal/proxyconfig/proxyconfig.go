package proxyconfig

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jeefyang/fnos_daili/internal/spec"
	"github.com/jeefyang/fnos_daili/internal/target"
)

type TLS struct {
	SkipVerify bool `json:"skip_verify"`
}

type Guard struct {
	AllowLoopback bool `json:"allow_loopback"`
}

type Timeouts struct {
	Dial           string `json:"dial"`
	TLSHandshake   string `json:"tls_handshake"`
	ResponseHeader string `json:"response_header"`
	Idle           string `json:"idle"`
}

type Config struct {
	Schema    int             `json:"schema"`
	AppName   string          `json:"appname"`
	Mode      string          `json:"mode"`
	Target    string          `json:"target"`
	TLS       TLS             `json:"tls"`
	Guard     Guard           `json:"guard"`
	BasicAuth *spec.BasicAuth `json:"basic_auth,omitempty"`
	Timeouts  Timeouts        `json:"timeouts"`
}

var bcryptCost12 = regexp.MustCompile(`^\$2[aby]\$12\$[./A-Za-z0-9]{53}$`)

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

func Parse(b []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("config is not valid JSON: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) Validate() error {
	if c.Schema != 1 {
		return fmt.Errorf("schema must be 1")
	}
	if err := spec.ValidateAppName(c.AppName); err != nil {
		return err
	}
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	if c.Mode != string(spec.ModeReverse) && c.Mode != string(spec.ModeDirect) {
		return fmt.Errorf("mode must be reverse or direct")
	}
	normalized, err := target.Normalize(c.Target)
	if err != nil {
		return fmt.Errorf("target: %w", err)
	}
	c.Target = normalized

	if c.BasicAuth != nil {
		if c.Mode == string(spec.ModeDirect) {
			return fmt.Errorf("basic_auth is only available in reverse mode")
		}
		if c.BasicAuth.Username == "" || len(c.BasicAuth.Username) > 64 {
			return fmt.Errorf("basic_auth username must be 1-64 bytes")
		}
		if !bcryptCost12.MatchString(c.BasicAuth.PasswordHash) {
			return fmt.Errorf("basic_auth password_hash must be a bcrypt cost-12 hash")
		}
	}
	if err := c.Timeouts.normalize(); err != nil {
		return err
	}
	return nil
}

func (c *Config) Direct() bool { return c.Mode == string(spec.ModeDirect) }

func (c *Config) TargetURL() (*url.URL, error) { return url.Parse(c.Target) }

func (t *Timeouts) normalize() error {
	defaults := Timeouts{Dial: "10s", TLSHandshake: "10s", ResponseHeader: "30s", Idle: "90s"}
	if t.Dial == "" {
		t.Dial = defaults.Dial
	}
	if t.TLSHandshake == "" {
		t.TLSHandshake = defaults.TLSHandshake
	}
	if t.ResponseHeader == "" {
		t.ResponseHeader = defaults.ResponseHeader
	}
	if t.Idle == "" {
		t.Idle = defaults.Idle
	}
	for name, value := range map[string]string{
		"dial":            t.Dial,
		"tls_handshake":   t.TLSHandshake,
		"response_header": t.ResponseHeader,
		"idle":            t.Idle,
	} {
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return fmt.Errorf("timeouts.%s must be a positive duration", name)
		}
	}
	return nil
}

func (t Timeouts) DialDuration() time.Duration           { return mustDuration(t.Dial) }
func (t Timeouts) TLSHandshakeDuration() time.Duration   { return mustDuration(t.TLSHandshake) }
func (t Timeouts) ResponseHeaderDuration() time.Duration { return mustDuration(t.ResponseHeader) }
func (t Timeouts) IdleDuration() time.Duration           { return mustDuration(t.Idle) }

func mustDuration(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	return d
}
