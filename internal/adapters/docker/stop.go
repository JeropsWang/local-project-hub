package docker

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
	"github.com/JeropsWang/local-project-hub/internal/ports"
)

var containerID = regexp.MustCompile(`^[0-9a-f]{64}$`)
var symbolicSignal = regexp.MustCompile(`^SIG[A-Z0-9]+([+-][0-9]{1,2})?$`)

type containerInspection struct {
	ID      string `json:"Id"`
	Created string
	Name    string
	Image   string
	Config  struct {
		StopSignal string
		Labels     map[string]string
	}
	State struct {
		Running   bool
		Paused    bool
		Dead      bool
		OOMKilled bool
		ExitCode  int
		StartedAt string
		Status    string
		Health    *struct{ Status string }
	}
}

func (a *NativeContainerAdapter) inspect(ctx context.Context, engine, id string) (containerInspection, error) {
	var result containerInspection
	if !containerID.MatchString(id) {
		return result, problem.New("CONTAINER_ID_INVALID", "")
	}
	err := a.request(ctx, engine, http.MethodGet, "/containers/"+id+"/json", &result)
	if err == nil && result.ID != id {
		err = problem.New("CONTAINER_IDENTITY_CHANGED", "")
	}
	return result, err
}

// Stop sends only the configured normal signal, then observes the original exact
// instance. It never invokes Engine's default stop timer or an implicit SIGKILL.
func (a *NativeContainerAdapter) Stop(ctx context.Context, instance run.Identity) (ports.ContainerStopResult, error) {
	var result ports.ContainerStopResult
	if a.authorization == nil {
		return result, problem.New("RESOURCE_CONTROL_NOT_GRANTED", "")
	}
	if err := a.authorization.AuthorizeContainer(ctx, instance); err != nil {
		return result, err
	}
	if instance.Kind != "container" || !containerID.MatchString(instance.ContainerID) ||
		!resourceUUID.MatchString(instance.ControlID) {
		return result, problem.New("CONTAINER_IDENTITY_INVALID", "")
	}
	if _, ok := ctx.Deadline(); !ok {
		bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		ctx = bounded
	}
	engineID, err := a.engineInfo(ctx, instance.Engine)
	if err != nil {
		return result, err
	}
	if engineID != instance.EngineID {
		return result, problem.New("DOCKER_ENGINE_CHANGED", "")
	}
	current, err := a.inspect(ctx, instance.Engine, instance.ContainerID)
	if err != nil {
		return result, err
	}
	if current.Created != instance.CreationTime || current.State.StartedAt != instance.StartedAt {
		return result, problem.New("CONTAINER_IDENTITY_CHANGED", "")
	}
	if !current.State.Running {
		return stoppedContainer(current, "", true), nil
	}
	if current.State.Paused {
		return ports.ContainerStopResult{Code: "CONTAINER_PAUSED"}, nil
	}
	signal, err := normalSignal(current.Config.StopSignal)
	if err != nil {
		return result, err
	}
	result.Signal = signal
	first, err := a.authorization.BeginContainerStop(ctx, instance, signal)
	if err != nil {
		return result, err
	}
	if first {
		if err := a.request(ctx, instance.Engine, http.MethodPost, "/containers/"+instance.ContainerID+"/kill?signal="+url.QueryEscape(signal), nil); err != nil {
			return ports.ContainerStopResult{Signal: signal, Code: "CONTAINER_SIGNAL_UNCERTAIN"}, nil
		}
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			result.Code = "CONTAINER_STOP_INCOMPLETE"
			return result, nil
		}
		current, err = a.inspect(ctx, instance.Engine, instance.ContainerID)
		if err != nil {
			result.Code = "CONTAINER_STOP_INCOMPLETE"
			return result, nil
		}
		if current.Created != instance.CreationTime || current.State.StartedAt != instance.StartedAt {
			return result, problem.New("CONTAINER_IDENTITY_CHANGED", "")
		}
		if !current.State.Running {
			return stoppedContainer(current, signal, false), nil
		}
		select {
		case <-ctx.Done():
			result.Code = "CONTAINER_STOP_INCOMPLETE"
			return result, nil
		case <-ticker.C:
		}
	}
}

func normalSignal(value string) (string, error) {
	if value == "" {
		return "SIGTERM", nil
	}
	value = strings.ToUpper(value)
	// Docker accepts numeric signal aliases. Normalize the optional SIG prefix
	// before numeric validation so SIG9 cannot bypass the SIGKILL exclusion.
	if number, err := strconv.Atoi(strings.TrimPrefix(value, "SIG")); err == nil {
		if number < 1 || number > 64 || number == 9 || number == 19 {
			return "", problem.New("CONTAINER_STOP_SIGNAL_UNSAFE", "")
		}
		return strconv.Itoa(number), nil
	}
	if !strings.HasPrefix(value, "SIG") {
		value = "SIG" + value
	}
	if !symbolicSignal.MatchString(value) || value == "SIGKILL" || value == "SIGSTOP" {
		return "", problem.New("CONTAINER_STOP_SIGNAL_UNSAFE", "")
	}
	return value, nil
}

func stoppedContainer(current containerInspection, signal string, already bool) ports.ContainerStopResult {
	result := ports.ContainerStopResult{Signal: signal, AlreadyStopped: already}
	if current.State.OOMKilled || current.State.Dead || current.State.ExitCode == 137 {
		result.Code = "CONTAINER_EXIT_FAILED"
		return result
	}
	if current.State.ExitCode != 0 && current.State.ExitCode != 143 && current.State.ExitCode != 130 {
		result.Code = "CONTAINER_EXIT_UNVERIFIED"
		return result
	}
	result.Graceful = true
	return result
}
