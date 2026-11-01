package link

import (
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Config is the links part of the node configuration. Other top-level keys of the node file are
// left to their own sections.
//
//	links:
//	  - id: lora
//	    driver: sim-mesh
//	    medium: field-a
//	    node: n1
//	relay:
//	  enabled: true
//	  accept_from: [lora]
//	  forward_via: [lte]
type Config struct {
	Links []LinkConfig `yaml:"links"`
	Relay RelayConfig  `yaml:"relay"`
}

// LinkConfig is one entry of links. Keys other than id and driver belong to the driver.
type LinkConfig struct {
	ID      string         `yaml:"id"`
	Driver  string         `yaml:"driver"`
	Options map[string]any `yaml:",inline"`
}

// RelayConfig says which links a relay node accepts neighbours' frames from and forwards them on.
type RelayConfig struct {
	Enabled    bool     `yaml:"enabled"`
	AcceptFrom []string `yaml:"accept_from"`
	ForwardVia []string `yaml:"forward_via"`
}

// ParseConfig reads and validates the links and relay sections of a node configuration.
func ParseConfig(data []byte) (Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("link: config: %w", err)
	}
	return cfg, cfg.Validate()
}

// Validate checks link ids, that every driver is registered and that relay names known links.
func (c Config) Validate() error {
	var errs []error
	seen := map[string]bool{}
	for i, l := range c.Links {
		switch {
		case strings.TrimSpace(l.ID) == "":
			errs = append(errs, fmt.Errorf("links[%d]: id is required", i))
		case seen[l.ID]:
			errs = append(errs, fmt.Errorf("links[%d]: duplicate id %q", i, l.ID))
		}
		seen[l.ID] = true
		switch {
		case l.Driver == "":
			errs = append(errs, fmt.Errorf("links[%d] %q: driver is required", i, l.ID))
		case !Registered(l.Driver):
			errs = append(errs, fmt.Errorf("links[%d] %q: unknown driver %q (known: %v)", i, l.ID, l.Driver, Drivers()))
		}
	}
	if c.Relay.Enabled {
		if len(c.Relay.AcceptFrom) == 0 || len(c.Relay.ForwardVia) == 0 {
			errs = append(errs, errors.New("relay: accept_from and forward_via are required when enabled"))
		}
		for _, id := range append(append([]string{}, c.Relay.AcceptFrom...), c.Relay.ForwardVia...) {
			if !seen[id] {
				errs = append(errs, fmt.Errorf("relay: unknown link %q", id))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("link: config: %w", errors.Join(errs...))
	}
	return nil
}

// Build creates one unopened driver per configured link, in configuration order.
func Build(cfg Config) ([]Driver, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	drivers := make([]Driver, 0, len(cfg.Links))
	for _, l := range cfg.Links {
		d, err := New(l)
		if err != nil {
			return nil, err
		}
		drivers = append(drivers, d)
	}
	return drivers, nil
}

// String reads an optional string option.
func (l LinkConfig) String(key, fallback string) (string, error) {
	v, ok := l.Options[key]
	if !ok || v == nil {
		return fallback, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: want a string, got %T", key, v)
	}
	return s, nil
}

// RequiredString reads a string option that must be set.
func (l LinkConfig) RequiredString(key string) (string, error) {
	s, err := l.String(key, "")
	if err == nil && s == "" {
		err = fmt.Errorf("%s is required", key)
	}
	return s, err
}
