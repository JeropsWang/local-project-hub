package docker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
)

// Engine API support is independent of a project's own Engine-version declaration.
const minimumEngineAPI = 45
const maximumEngineAPI = 53

type engineAPICache struct {
	mu      sync.Mutex
	version string
	expires time.Time
}

func (c engineConnection) apiVersion(ctx context.Context) (string, error) {
	if c.api == nil {
		return engineAPIVersion, nil
	}
	c.api.mu.Lock()
	defer c.api.mu.Unlock()
	if c.api.version != "" && time.Now().Before(c.api.expires) {
		return c.api.version, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/version", nil)
	if err != nil {
		return "", problem.New("DOCKER_REQUEST_INVALID", "")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return "", problem.New("DOCKER_CONNECTION_FAILED", "")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", problem.New("DOCKER_API_UNSUPPORTED", "")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return "", problem.New("DOCKER_RESPONSE_INVALID", "")
	}
	var descriptor struct {
		Maximum string `json:"ApiVersion"`
		Minimum string `json:"MinAPIVersion"`
	}
	if json.Unmarshal(data, &descriptor) != nil {
		return "", problem.New("DOCKER_RESPONSE_INVALID", "")
	}
	maximum, ok := engineAPIMinor(descriptor.Maximum)
	if !ok {
		return "", problem.New("DOCKER_API_UNSUPPORTED", "")
	}
	minimum := 0
	if descriptor.Minimum != "" {
		var valid bool
		minimum, valid = engineAPIMinor(descriptor.Minimum)
		if !valid {
			return "", problem.New("DOCKER_API_UNSUPPORTED", "")
		}
	}
	selected := min(maximum, maximumEngineAPI)
	if selected < minimumEngineAPI || selected < minimum {
		return "", problem.New("DOCKER_API_UNSUPPORTED", "")
	}
	c.api.version = "v1." + strconv.Itoa(selected)
	c.api.expires = time.Now().Add(30 * time.Second)
	return c.api.version, nil
}

func engineAPIMinor(value string) (int, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] != "1" || len(parts[1]) < 1 || len(parts[1]) > 3 {
		return 0, false
	}
	for _, digit := range parts[1] {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	minor, err := strconv.Atoi(parts[1])
	return minor, err == nil
}
