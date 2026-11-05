package process

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

func TestControlCredentialUsesCurrentUserProtectionAndRunEntropy(t *testing.T) {
	root := t.TempDir()
	anchor, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer anchor.Close()
	identity := run.Identity{
		Kind:         "process",
		PID:          123,
		CreationTime: "fixture",
		Executable:   `C:\fixture.exe`,
		ControlID:    fixtureControlRun,
	}
	if err := saveControl(anchor, identity, fixtureControlToken); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "secrets", "controls", fixtureControlRun+".dpapi"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(fixtureControlToken)) {
		t.Fatal("plaintext runtime token persisted")
	}
	for _, id := range []string{fixtureControlRun, fixtureOperation} {
		entropy := []byte("LocalProjectHub/control/v1/" + id)
		input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
		extra := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
		var output windows.DataBlob
		err := windows.CryptUnprotectData(&input, nil, &extra, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
		if id != fixtureControlRun {
			if err == nil {
				windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
				t.Fatal("credential unbound from run")
			}
			continue
		}
		if err != nil {
			t.Fatal("same-user credential unavailable", err)
		}
		plain := unsafe.Slice(output.Data, output.Size)
		if !bytes.Contains(plain, []byte(fixtureControlToken)) {
			t.Fatal("wrong protected payload")
		}
		clear(plain)
		windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	}
}
