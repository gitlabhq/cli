package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/shlex"
	"go.yaml.in/yaml/v3"
)

const customHeaderCommandTimeout = 30 * time.Second

// CustomHeader represents a single custom HTTP header configuration
type CustomHeader struct {
	Name             string `yaml:"name"`
	Value            string `yaml:"value,omitempty"`
	ValueFromEnv     string `yaml:"valueFromEnv,omitempty"`
	ValueFromCommand string `yaml:"valueFromCommand,omitempty"`
}

type customHeaderCommandResult struct {
	once  sync.Once
	value string
	err   error
}

// ResolvedValue returns the actual header value, resolving environment variables or commands if needed.
func (h *CustomHeader) ResolvedValue() (string, error) {
	switch {
	case h.ValueFromCommand != "":
		return resolveCustomHeaderCommand(h.ValueFromCommand)
	case h.ValueFromEnv != "":
		value := os.Getenv(h.ValueFromEnv)
		if value == "" {
			return "", fmt.Errorf("environment variable %q for header %q is not set or empty", h.ValueFromEnv, h.Name)
		}
		return value, nil
	case h.Value != "":
		return h.Value, nil
	default:
		return "", errors.New("exactly one of value, valueFromEnv, or valueFromCommand must be specified for a custom header")
	}
}

func resolveCustomHeaderCommand(command string) (string, error) {
	return resolveCustomHeaderCommandWithTimeout(command, customHeaderCommandTimeout)
}

func resolveCustomHeaderCommandWithTimeout(command string, timeout time.Duration) (string, error) {
	args, err := shlex.Split(command)
	if err != nil {
		return "", fmt.Errorf("parsing command: %w", err)
	}
	if len(args) == 0 {
		return "", errors.New("command is empty")
	}

	cmd := exec.Command(args[0], args[1:]...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("running command: %w", err)
	}

	timedOut := false
	timerDone := make(chan struct{})
	timer := time.AfterFunc(timeout, func() {
		defer close(timerDone)
		// Kill the whole process group/tree, not just the directly-spawned
		// process: shell wrappers such as `sh -c '...'` (the README's
		// documented pattern for pipelines) leave their real work running in
		// a child that would otherwise be orphaned when only the wrapper is
		// killed.
		if err := killProcessGroup(cmd); err == nil {
			timedOut = true
		}
	})
	err = cmd.Wait()
	if !timer.Stop() {
		<-timerDone
	}
	if timedOut {
		return "", fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return "", fmt.Errorf("running command: %w: %s", err, message)
		}
		return "", fmt.Errorf("running command: %w", err)
	}

	value := strings.TrimSpace(stdout.String())
	if value == "" {
		return "", errors.New("command returned an empty value")
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("command returned a value containing a newline or NUL byte")
	}

	return value, nil
}

// GetCustomHeaders returns the custom headers for a specific host
func (hc *HostConfig) GetCustomHeaders() ([]CustomHeader, error) {
	entry, err := hc.FindEntry("custom_headers")
	if err != nil {
		if isNotFoundError(err) {
			// No custom headers configured, return nil slice
			return nil, nil
		}

		return nil, err
	}

	if entry.ValueNode.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("custom_headers must be a list")
	}

	var headers []CustomHeader
	for _, headerNode := range entry.ValueNode.Content {
		if headerNode.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("each custom header must be a mapping with 'name' and exactly one of 'value', 'valueFromEnv', or 'valueFromCommand'")
		}

		var header CustomHeader
		if err := headerNode.Decode(&header); err != nil {
			return nil, fmt.Errorf("failed to decode custom header: %w", err)
		}

		// Validate header configuration
		if header.Name == "" {
			return nil, fmt.Errorf("custom header must have a 'name' field")
		}
		sourceCount := 0
		for _, value := range []string{header.Value, header.ValueFromEnv, header.ValueFromCommand} {
			if value != "" {
				sourceCount++
			}
		}
		if sourceCount != 1 {
			return nil, fmt.Errorf("custom header %q must have exactly one of 'value', 'valueFromEnv', or 'valueFromCommand'", header.Name)
		}

		headers = append(headers, header)
	}

	return headers, nil
}

// ResolveCustomHeaders returns a map of resolved custom headers for a host
func (c *fileConfig) ResolveCustomHeaders(hostname string) (map[string]string, error) {
	if hostname == "" {
		return nil, nil
	}

	hostCfg, err := c.configForHost(hostname)
	if err != nil {
		if isNotFoundError(err) {
			// Host not configured, return empty map
			return nil, nil
		}

		return nil, err
	}

	headers, err := hostCfg.GetCustomHeaders()
	if err != nil {
		return nil, err
	}

	resolved := make(map[string]string, len(headers))
	for _, header := range headers {
		value, err := c.resolveCustomHeaderValue(hostname, &header)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve header %q: %w", header.Name, err)
		}
		resolved[header.Name] = value
	}

	return resolved, nil
}

func (c *fileConfig) resolveCustomHeaderValue(hostname string, header *CustomHeader) (string, error) {
	if header.ValueFromCommand == "" {
		return header.ResolvedValue()
	}

	key := strings.Join([]string{hostname, header.Name, header.ValueFromCommand}, "\x00")
	resultValue, _ := c.customHeaderCommandValues.LoadOrStore(key, &customHeaderCommandResult{})
	result, ok := resultValue.(*customHeaderCommandResult)
	if !ok {
		return "", errors.New("invalid cached custom header command result")
	}
	result.once.Do(func() {
		result.value, result.err = header.ResolvedValue()
	})
	return result.value, result.err
}

// ResolveCustomHeaders is a helper function that works with the Config interface
func ResolveCustomHeaders(cfg Config, hostname string) (map[string]string, error) {
	// Try to get the fileConfig implementation
	fc, ok := cfg.(*fileConfig)
	if !ok {
		// Not a fileConfig, this is an unexpected condition
		return nil, fmt.Errorf("unexpected config type: %T, expected *fileConfig", cfg)
	}

	return fc.ResolveCustomHeaders(hostname)
}
