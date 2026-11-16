package target

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Normalize validates raw and returns a canonical origin.
// Only scheme://host[:port] is accepted; path must be empty or "/".
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("target is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("target is not a valid URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("target scheme must be http or https")
	}
	if u.User != nil {
		return "", fmt.Errorf("target must not contain userinfo")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("target must not contain a path")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("target must not contain a query")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return "", fmt.Errorf("target must not contain a fragment")
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("target host is empty")
	}
	if strings.Contains(host, "%") {
		return "", fmt.Errorf("target must not contain an IPv6 zone")
	}

	var renderedHost string
	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		if addr.Is4() {
			renderedHost = addr.String()
		} else {
			renderedHost = "[" + addr.String() + "]"
		}
	} else {
		if strings.Contains(host, ":") {
			return "", fmt.Errorf("target IPv6 address is invalid")
		}
		host = strings.TrimSuffix(host, ".")
		if err := validateDomain(host); err != nil {
			return "", err
		}
		renderedHost = host
	}

	defaultPort := 80
	if scheme == "https" {
		defaultPort = 443
	}
	port := u.Port()
	if port == "" {
		port = strconv.Itoa(defaultPort)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("target port must be between 1 and 65535")
	}
	if n == defaultPort {
		return scheme + "://" + renderedHost, nil
	}
	return scheme + "://" + renderedHost + ":" + strconv.Itoa(n), nil
}

func validateDomain(host string) error {
	if host == "" {
		return fmt.Errorf("target host is empty")
	}
	if len(host) > 253 {
		return fmt.Errorf("target host is too long")
	}
	labels := strings.Split(host, ".")
	allNumeric := true
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("target host has an invalid label")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("target host has an invalid label")
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return fmt.Errorf("target host must be ASCII letters, digits, dots, or hyphens")
			}
			if c < '0' || c > '9' {
				allNumeric = false
			}
		}
	}
	if allNumeric {
		return fmt.Errorf("target host must be a dotted-decimal IPv4 address or a domain name")
	}
	return nil
}
