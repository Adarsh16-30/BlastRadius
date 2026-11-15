package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Adarsh16-30/BlastRadius/internal/store"
)

type config struct {
	Listen       string
	DB           store.Config
	ChaosEnabled bool
}

// loadConfig reads and validates the environment; getenv is injected for tests.
func loadConfig(getenv func(string) string) (config, error) {
	c := config{
		Listen: orDefault(getenv("LISTEN_ADDR"), ":8080"),
		DB: store.Config{
			Name:     orDefault(getenv("DB_NAME"), "blastradius"),
			User:     getenv("DB_USER"),
			Password: getenv("DB_PASSWORD"),
		},
	}
	var errs []error
	for _, h := range strings.Split(getenv("DB_HOSTS"), ",") {
		if err := validHostPort(strings.TrimSpace(h)); err != nil {
			errs = append(errs, fmt.Errorf("DB_HOSTS: %w", err))
			break
		}
		c.DB.Hosts = append(c.DB.Hosts, strings.TrimSpace(h))
	}
	if c.DB.User == "" {
		errs = append(errs, errors.New("DB_USER is required"))
	}
	if c.DB.Password == "" {
		errs = append(errs, errors.New("DB_PASSWORD is required"))
	}
	if v := getenv("CHAOS_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("CHAOS_ENABLED must be true or false, got %q", v))
		}
		c.ChaosEnabled = b
	}
	return c, errors.Join(errs...)
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func validHostPort(s string) error {
	i := strings.LastIndex(s, ":")
	if i < 1 {
		return fmt.Errorf("%q must be host:port", s)
	}
	if p, err := strconv.Atoi(s[i+1:]); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("%q has an invalid port", s)
	}
	return nil
}
