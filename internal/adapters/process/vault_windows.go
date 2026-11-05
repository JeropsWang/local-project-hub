package process

import (
	"encoding/json"
	"os"
	"path"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/JeropsWang/local-project-hub/internal/domain/control"
	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"github.com/JeropsWang/local-project-hub/internal/domain/run"
)

type protectedControl struct {
	Identity run.Identity `json:"identity"`
	Token    string       `json:"token"`
}

// saveControl uses CurrentUser DPAPI with run-specific entropy. A copied encrypted
// credential is not a portable grant and cannot authorize a PID belonging to another run.
func saveControl(root *os.Root, identity run.Identity, token string) error {
	if !control.ValidIdentity(identity.ControlID) || !control.ValidToken(token) {
		return problem.New("CONTROL_CREDENTIAL_INVALID", "")
	}
	data, err := json.Marshal(protectedControl{Identity: identity, Token: token})
	if err != nil {
		return err
	}
	defer clear(data)
	entropy := []byte("LocalProjectHub/control/v1/" + identity.ControlID)
	input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	extra := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, &extra, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return problem.New("CONTROL_CREDENTIAL_PROTECTION_FAILED", "")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	if err := root.MkdirAll("secrets/controls", 0700); err != nil {
		return err
	}
	file, err := root.OpenFile(
		path.Join("secrets/controls", identity.ControlID+".dpapi"),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0600,
	)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(unsafe.Slice(output.Data, output.Size))
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
