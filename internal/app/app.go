// Package app puts the service together from its configuration: each
// group's provider, with its credentials read from the secrets the
// configuration names (specs/0001-service.md).
package app

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/lanescape/downloads/config"
	"github.com/lanescape/downloads/internal/providers/r2"
	"github.com/lanescape/downloads/internal/secrets"
	"github.com/lanescape/downloads/internal/server"
)

// Options are what differs between Cloud Run, a local run and a test.
type Options struct {
	// Secrets gives the values of the secrets the configuration names.
	Secrets secrets.Source
	// R2Endpoint, if set, replaces every R2 group's endpoint,
	// https://<account ID>.r2.cloudflarestorage.com, as an emulator's.
	R2Endpoint string
	Log        *slog.Logger
	Now        func() time.Time
}

// New is the service's handler. It fails, naming the secret, if any secret
// the configuration names has no value.
func New(c *config.Config, o Options) (*server.Server, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	var groups []server.Group
	for _, g := range c.Groups {
		p, err := provider(g, c.URLLifetime, o)
		if err != nil {
			return nil, fmt.Errorf("group %s: %w", g.Path, err)
		}
		groups = append(groups, server.Group{Path: g.Path, Provider: p})
	}
	return server.New(groups, o.Log, o.Now), nil
}

func provider(g config.Group, lifetime time.Duration, o Options) (server.Provider, error) {
	id, err := o.Secrets.Get(g.R2.AccessKeyIDSecretName)
	if err != nil {
		return nil, err
	}
	secret, err := o.Secrets.Get(g.R2.SecretAccessKeySecretName)
	if err != nil {
		return nil, err
	}
	endpoint := r2.Endpoint(g.R2.AccountID)
	if o.R2Endpoint != "" {
		endpoint = o.R2Endpoint
	}
	return r2.New(endpoint, g.R2.Bucket, id, secret, lifetime)
}
