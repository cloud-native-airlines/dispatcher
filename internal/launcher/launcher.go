// Package launcher runs one Flight container per leg through the Docker API,
// the compose stand-in for "a pod/Job per leg". It attaches each container to the
// configured network so it resolves nats/adsb/operations by name.
package launcher

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Spec describes one leg's container.
type Spec struct {
	Name     string
	Image    string
	Env      map[string]string
	Labels   map[string]string
	Network  string // docker network to attach to; "" leaves the default
	AutoRemove bool
}

type Launcher struct {
	cli *client.Client
}

// New creates a launcher using the ambient Docker environment (DOCKER_HOST, or
// the mounted /var/run/docker.sock).
func New() (*Launcher, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Launcher{cli: cli}, nil
}

// Launch creates and starts the container, returning its id.
func (l *Launcher) Launch(ctx context.Context, s Spec) (string, error) {
	cfg := &container.Config{
		Image:  s.Image,
		Env:    envSlice(s.Env),
		Labels: s.Labels,
	}
	hostCfg := &container.HostConfig{AutoRemove: s.AutoRemove}
	if s.Network != "" {
		hostCfg.NetworkMode = container.NetworkMode(s.Network)
	}
	resp, err := l.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, sanitizeName(s.Name))
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}
	if err := l.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return resp.ID, fmt.Errorf("start container: %w", err)
	}
	return resp.ID, nil
}

// Close releases the Docker client.
func (l *Launcher) Close() error { return l.cli.Close() }

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

var nameInvalid = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func sanitizeName(name string) string {
	return strings.Trim(nameInvalid.ReplaceAllString(name, "-"), "-")
}
