// Package docker adapts local Docker Engine/Compose without coupling application use cases to the API.
package docker

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	winio "github.com/Microsoft/go-winio"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

const engineAPIVersion = "v1.45"

var localPipe = regexp.MustCompile(`^\\\\\.\\pipe\\[a-zA-Z0-9_.-]{1,128}$`)

type engineConnection struct {
	baseURL string
	client  *http.Client
	pipe    string
	api     *engineAPICache
}

// NativeContainerAdapter accepts configured local endpoints; input JSON cannot add arbitrary URLs.
type NativeContainerAdapter struct {
	engines       map[string]engineConnection
	authorization ports.ContainerAuthorization
	workspace     string
	runner        ports.ComposeRunner
}

// NewContainerAdapter connects only through explicitly selected local Windows named pipes.
func NewContainerAdapter(
	workspace string,
	pipes map[string]string,
	authorization ports.ContainerAuthorization,
	runner ports.ComposeRunner,
) (*NativeContainerAdapter, error) {
	engines := map[string]engineConnection{}
	for name, pipe := range pipes {
		if !composeKey.MatchString(name) || !localPipe.MatchString(pipe) {
			return nil, problem.New("DOCKER_ENDPOINT_UNSUPPORTED", "")
		}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return winio.DialPipeContext(ctx, pipe) },
		}
		engines[name] = engineConnection{
			baseURL: "http://docker",
			client:  &http.Client{Transport: transport, Timeout: 30 * time.Second},
			pipe:    pipe,
			api:     &engineAPICache{},
		}
	}
	adapter := newContainerAdapter(engines, authorization)
	adapter.workspace = workspace
	adapter.runner = runner
	return adapter, nil
}

func newContainerAdapter(
	engines map[string]engineConnection,
	authorization ports.ContainerAuthorization,
) *NativeContainerAdapter {
	return &NativeContainerAdapter{engines: engines, authorization: authorization}
}

func (a *NativeContainerAdapter) request(ctx context.Context, engine, method, relative string, result any) error {
	connection, ok := a.engines[engine]
	if !ok {
		return problem.New("DOCKER_ENGINE_BINDING_REQUIRED", engine)
	}
	version, err := connection.apiVersion(ctx)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, connection.baseURL+"/"+version+relative, nil)
	if err != nil {
		return problem.New("DOCKER_REQUEST_INVALID", "")
	}
	response, err := connection.client.Do(request)
	if err != nil {
		return problem.New("DOCKER_CONNECTION_FAILED", "")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == 404 {
			return problem.New("CONTAINER_INSTANCE_MISSING", "")
		}
		return problem.New("DOCKER_API_REQUEST_FAILED", "")
	}
	if result == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
		return err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024+1))
	if err != nil {
		return problem.New("DOCKER_RESPONSE_INVALID", "")
	}
	defer clear(data)
	if len(data) > 16*1024*1024 || json.Unmarshal(data, result) != nil {
		return problem.New("DOCKER_RESPONSE_INVALID", "")
	}
	return nil
}

func (a *NativeContainerAdapter) engineInfo(ctx context.Context, engine string) (string, error) {
	var info struct {
		ID     string
		OSType string
	}
	if err := a.request(ctx, engine, http.MethodGet, "/info", &info); err != nil {
		return "", err
	}
	if info.ID == "" || len(info.ID) > 256 || strings.ContainsRune(info.ID, 0) || info.OSType != "linux" {
		return "", problem.New("DOCKER_ENGINE_UNSUPPORTED", "")
	}
	return info.ID, nil
}
