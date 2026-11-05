package filesystem

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"unsafe"

	"github.com/JeropsWang/local-project-hub/internal/domain/problem"
	"golang.org/x/sys/windows"
)

const ownerFile = ".local-owner.dpapi"

// LocalOwnerMatches binds local authority to both machine and Windows user,
// independently of path/portable UUID. Two DPAPI layers prevent a roaming user
// profile alone from carrying a workspace's machine-control grants.
func (d *Directory) LocalOwnerMatches(id string) (bool, error) {
	info, err := d.Root.Lstat(ownerFile)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return false, problem.New("WORKSPACE_OWNER_FILE_INVALID", "")
	}
	file, err := d.Root.Open(ownerFile)
	if err != nil {
		return false, err
	}
	data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	file.Close()
	if err != nil || len(data) > 64*1024 {
		return false, problem.New("WORKSPACE_OWNER_FILE_INVALID", "")
	}
	entropy := []byte("LocalProjectHub/workspace-owner/v1/" + id)
	outer, err := ownerCrypt(data, entropy, false, 0)
	if err != nil {
		return false, nil
	}
	defer clear(outer)
	inner, err := ownerCrypt(outer, entropy, false, 0)
	if err != nil {
		return false, nil
	}
	defer clear(inner)
	return bytes.Equal(inner, []byte("LocalProjectHub/workspace-owner/v1/"+id)), nil
}

// WriteLocalOwner publishes only after stale local grants have been invalidated.
// If publication fails, opening the workspace still requires rebind; it cannot
// regain old authority through a half-committed host identity change.
func (d *Directory) WriteLocalOwner(id string) error {
	if info, err := d.Root.Lstat(ownerFile); err == nil && !info.Mode().IsRegular() {
		return problem.New("WORKSPACE_OWNER_FILE_INVALID", "")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	data := []byte("LocalProjectHub/workspace-owner/v1/" + id)
	entropy := append([]byte{}, data...)
	inner, err := ownerCrypt(data, entropy, true, windows.CRYPTPROTECT_UI_FORBIDDEN)
	if err != nil {
		return err
	}
	defer clear(inner)
	outer, err := ownerCrypt(inner, entropy, true, windows.CRYPTPROTECT_UI_FORBIDDEN|windows.CRYPTPROTECT_LOCAL_MACHINE)
	if err != nil {
		return err
	}
	defer clear(outer)
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	stage := ownerFile + ".staging-" + hex.EncodeToString(nonce[:])
	file, err := d.Root.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer d.Root.Remove(stage)
	_, writeErr := file.Write(outer)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err = errorsJoin(writeErr, errorsJoin(syncErr, closeErr)); err != nil {
		return err
	}
	return d.Root.Rename(stage, ownerFile)
}

func ownerCrypt(data, entropy []byte, protect bool, flags uint32) ([]byte, error) {
	if len(data) == 0 || len(entropy) == 0 {
		return nil, problem.New("WORKSPACE_OWNER_INVALID", "")
	}
	input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	extra := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var output windows.DataBlob
	var err error
	if protect {
		err = windows.CryptProtectData(&input, nil, &extra, 0, nil, flags, &output)
	} else {
		err = windows.CryptUnprotectData(&input, nil, &extra, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	}
	if err != nil {
		return nil, problem.New("WORKSPACE_OWNER_UNVERIFIABLE", "")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte{}, unsafe.Slice(output.Data, output.Size)...), nil
}
