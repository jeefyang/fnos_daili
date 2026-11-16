package spec

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jeefyang/fnos_daili/internal/target"
)

const (
	ManagerAppName = "daili.manager"
	PortMin        = 20000
	PortMax        = 29999
)

// ValidPort reports whether p is in the manager-allocated proxy port range.
func ValidPort(p int) bool { return p >= PortMin && p <= PortMax }

type Mode string

const (
	ModeReverse Mode = "reverse"
	ModeDirect  Mode = "direct"
)

type BasicAuth struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

// ProxySpec is the user-editable description of one generated proxy app.
type ProxySpec struct {
	ID            string     `json:"id"`
	AppName       string     `json:"appname"`
	DisplayName   string     `json:"display_name"`
	Description   string     `json:"description"`
	Version       string     `json:"version"`
	TargetURL     string     `json:"target_url"`
	Mode          string     `json:"mode"`
	EntryTitle    string     `json:"entry_title"`
	UIType        string     `json:"ui_type"`
	Port          int        `json:"port"`
	AllUsers      bool       `json:"all_users"`
	NoDisplay     bool       `json:"no_display"`
	FileTypes     []string   `json:"file_types"`
	InstallVolume string     `json:"install_volume"`
	SkipTLSVerify bool       `json:"skip_tls_verify"`
	AllowLoopback bool       `json:"allow_loopback"`
	BasicAuth     *BasicAuth `json:"basic_auth,omitempty"`
}

type Error struct {
	Field   string
	Message string
}

func (e *Error) Error() string { return e.Field + ": " + e.Message }

func invalid(field, format string, args ...any) error {
	return &Error{Field: field, Message: fmt.Sprintf(format, args...)}
}

var (
	appNameRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	versionRE    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$`)
	volumeRE     = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	bcryptCost12 = regexp.MustCompile(`^\$2[aby]\$12\$[./A-Za-z0-9]{53}$`)
)

// NormalizeProxySpec validates fields and fills defaults. It does not persist anything.
func ValidateAppName(appname string) error {
	if len(appname) < 3 || len(appname) > 32 || !appNameRE.MatchString(appname) {
		return invalid("appname", "must be 3-32 bytes, start with a letter or digit, and contain only letters, digits, '.', '_' or '-'")
	}
	if appname == ManagerAppName {
		return invalid("appname", "is reserved for the manager")
	}
	return nil
}

func NormalizeProxySpec(in ProxySpec) (ProxySpec, error) {
	out := in

	if err := ValidateAppName(out.AppName); err != nil {
		return out, err
	}

	if !validText(out.DisplayName, 1, 32) {
		return out, invalid("display_name", "must be 1-32 characters without line breaks")
	}
	if !validText(out.Description, 0, 120) {
		return out, invalid("description", "must be 0-120 characters without line breaks")
	}

	if out.Version == "" {
		out.Version = "1.0.0"
	}
	if len(out.Version) > 32 || !versionRE.MatchString(out.Version) {
		return out, invalid("version", "must look like 1.0.0")
	}

	if strings.TrimSpace(out.TargetURL) == "" {
		return out, invalid("target_url", "is required")
	}
	targetURL, err := target.Normalize(out.TargetURL)
	if err != nil {
		return out, invalid("target_url", "%v", err)
	}
	out.TargetURL = targetURL

	if out.Mode == "" {
		out.Mode = string(ModeReverse)
	}
	out.Mode = strings.ToLower(out.Mode)
	if out.Mode != string(ModeReverse) && out.Mode != string(ModeDirect) {
		return out, invalid("mode", "must be reverse or direct")
	}

	if out.UIType == "" {
		out.UIType = "url"
	}
	out.UIType = strings.ToLower(out.UIType)
	if out.UIType != "url" && out.UIType != "iframe" {
		return out, invalid("ui_type", "must be url or iframe")
	}

	if out.EntryTitle == "" {
		out.EntryTitle = out.DisplayName
	}
	if !validText(out.EntryTitle, 1, 32) {
		return out, invalid("entry_title", "must be 1-32 characters without line breaks")
	}

	if out.InstallVolume != "" && !volumeRE.MatchString(out.InstallVolume) {
		return out, invalid("install_volume", "must contain only letters, digits, '.', '_' or '-'")
	}

	if out.Port != 0 && !ValidPort(out.Port) {
		return out, invalid("port", "must be between %d and %d", PortMin, PortMax)
	}

	if out.BasicAuth != nil {
		if out.Mode == string(ModeDirect) {
			return out, invalid("basic_auth", "is only available in reverse mode")
		}
		if out.BasicAuth.Username == "" || len(out.BasicAuth.Username) > 64 {
			return out, invalid("basic_auth", "username must be 1-64 bytes")
		}
		if !bcryptCost12.MatchString(out.BasicAuth.PasswordHash) {
			return out, invalid("basic_auth", "password_hash must be a bcrypt cost-12 hash")
		}
	}

	return out, nil
}

func validText(s string, min, max int) bool {
	if strings.ContainsAny(s, "\r\n") {
		return false
	}
	n := utf8.RuneCountInString(s)
	return n >= min && n <= max
}
