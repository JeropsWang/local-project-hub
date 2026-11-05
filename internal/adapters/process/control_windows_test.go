package process

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

const fixtureControlRun = "55555555-5555-4555-8555-555555555555"
const fixtureOperation = "66666666-6666-4666-8666-666666666666"
const fixtureControlToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func replyClient(t *testing.T, replies string) *ControlClient {
	t.Helper()
	return &ControlClient{dial: func(ctx context.Context, instance run.Identity) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			line, err := bufio.NewReader(server).ReadBytes('\n')
			if err != nil {
				return
			}
			var request map[string]string
			if json.Unmarshal(line, &request) != nil || request["token"] != fixtureControlToken {
				return
			}
			_, _ = server.Write([]byte(replies))
		}()
		return client, nil
	}}
}

func TestControlRejectsWrongIdentityAndAckThenCrash(t *testing.T) {
	for _, tc := range []struct{ name, replies, code string }{
		{"wrong-instance", `{"protocolVersion":"1.0","runId":"77777777-7777-4777-8777-777777777777","operationId":"` + fixtureOperation + `","kind":"completed","state":"stopped"}` + "\n", "CONTROL_RESPONSE_INVALID"},
		{"ack-crash", `{"protocolVersion":"1.0","runId":"` + fixtureControlRun + `","operationId":"` + fixtureOperation + `","kind":"accepted","state":"saving"}` + "\n", "CONTROL_DISCONNECTED"},
		{"unknown-field", `{"protocolVersion":"1.0","runId":"` + fixtureControlRun + `","operationId":"` + fixtureOperation + `","kind":"completed","state":"stopped","token":"must-not-echo"}` + "\n", "CONTROL_RESPONSE_INVALID"},
		{"duplicate-field", `{"protocolVersion":"1.0","runId":"` + fixtureControlRun + `","operationId":"` + fixtureOperation + `","kind":"completed","state":"saving","state":"stopped"}` + "\n", "CONTROL_RESPONSE_INVALID"},
		{"oversize", strings.Repeat("x", 16*1024+1) + "\n", "CONTROL_FRAME_LIMIT"},
		{"missing-error-message", `{"protocolVersion":"1.0","runId":"` + fixtureControlRun + `","operationId":"` + fixtureOperation + `","kind":"rejected","state":"failed","error":{"code":"SAVE_FAILED"}}` + "\n", "CONTROL_RESPONSE_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			outcome, err := replyClient(t, tc.replies).Exchange(ctx, run.Identity{ControlID: fixtureControlRun}, fixtureControlToken, fixtureOperation, "shutdown")
			if err == nil || err.Error() != tc.code || outcome.Completed {
				t.Fatalf("false completion: %+v %v", outcome, err)
			}
		})
	}
}

func TestControlCompletionAndBlockedResultsRemainDistinct(t *testing.T) {
	for _, tc := range []struct {
		name, kind, state, code string
		complete                bool
	}{
		{"saved", "completed", "stopped", "", true},
		{"pending", "rejected", "blocked", "WORK_PENDING", false},
		{"save-failed", "rejected", "failed", "SAVE_FAILED", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"protocolVersion": "1.0", "runId": fixtureControlRun, "operationId": fixtureOperation, "kind": tc.kind, "state": tc.state}
			if tc.code != "" {
				body["error"] = map[string]string{"code": tc.code, "message": "private application details"}
			}
			encoded, _ := json.Marshal(body)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := replyClient(t, string(encoded)+"\n").Exchange(ctx, run.Identity{ControlID: fixtureControlRun}, fixtureControlToken, fixtureOperation, "shutdown")
			if err != nil || result.Completed != tc.complete || result.State != tc.state || result.ErrorCode != tc.code {
				t.Fatalf("outcome %+v %v", result, err)
			}
		})
	}
}
