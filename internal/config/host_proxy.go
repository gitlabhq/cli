package config

import (
	"fmt"
	"net/url"
	"strings"
)

// resolveHostProxy retrieves the proxy value for a given host, if set.
func (c *fileConfig) resolveHostProxy(hostname string) (*url.URL, error) {
	value, err := c.hostProxyValue(hostname)
	if err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("failed to parse proxy URL %q: %w", trimmed, err)
	}

	return parsed, nil
}

// hostProxyValue avoids GetWithSource's root-level fallback: proxy is a
// per-host key, and a top-level value has never applied to hosts.
func (c *fileConfig) hostProxyValue(hostname string) (string, error) {
	if hostname == "" {
		return "", nil
	}

	if value, _ := c.envValueForHost(hostname, "proxy"); value != "" {
		return value, nil
	}

	hostCfg, err := c.configForHost(hostname)
	if err != nil {
		if isNotFoundError(err) {
			return "", nil
		}
		return "", err
	}

	value, err := hostCfg.GetStringValue("proxy")
	if err != nil && !isNotFoundError(err) {
		return "", err
	}
	return value, nil
}

// ResolveHostProxy exposes per-host proxy resolution for consumers.
func ResolveHostProxy(cfg Config, hostname string) (*url.URL, error) {
	fc, ok := cfg.(*fileConfig)
	if !ok {
		return nil, fmt.Errorf("unexpected config type: %T, expected *fileConfig", cfg)
	}

	return fc.resolveHostProxy(hostname)
}
