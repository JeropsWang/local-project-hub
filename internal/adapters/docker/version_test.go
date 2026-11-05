package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEngineAPINegotiationBoundsAndRejectsUnsupportedVersions(t *testing.T) {
	for _, tc := range []struct{ maximum, minimum, want, code string }{
		{"1.60", "1.44", "v1.53", ""},
		{"1.47", "1.24", "v1.47", ""},
		{"1.44", "1.24", "", "DOCKER_API_UNSUPPORTED"},
		{"1.60", "1.54", "", "DOCKER_API_UNSUPPORTED"},
	} {
		t.Run(tc.maximum+"-"+tc.minimum, func(t *testing.T) {
			var requested string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/version" {
					_ = json.NewEncoder(w).
						Encode(map[string]string{"ApiVersion": tc.maximum, "MinAPIVersion": tc.minimum})
					return
				}
				requested = r.URL.Path
				_ = json.NewEncoder(w).Encode(map[string]string{"ID": "fixture", "OSType": "linux"})
			}))
			defer server.Close()
			adapter := newContainerAdapter(
				map[string]engineConnection{
					"test": {baseURL: server.URL, client: server.Client(), api: &engineAPICache{}},
				},
				nil,
			)
			_, err := adapter.engineInfo(context.Background(), "test")
			if tc.code != "" {
				if err == nil || err.Error() != tc.code || requested != "" {
					t.Fatalf("unsupported API used: %s %v", requested, err)
				}
				return
			}
			if err != nil || requested != "/"+tc.want+"/info" {
				t.Fatalf("negotiated path %q error %v", requested, err)
			}
		})
	}
}
