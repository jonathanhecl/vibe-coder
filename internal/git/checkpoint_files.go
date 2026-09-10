package git

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/safety"
)

const maxCheckpointBytes = 32 << 20

type checkpointRevision struct {
	Exists bool        `json:"exists"`
	Mode   os.FileMode `json:"mode"`
	Hash   [32]byte    `json:"hash"`
}

type checkpointState struct {
	Exists bool        `json:"exists"`
	Mode   os.FileMode `json:"mode"`
	Data   []byte      `json:"data"`
}

func (s checkpointState) revision() checkpointRevision {
	return checkpointRevision{Exists: s.Exists, Mode: s.Mode, Hash: sha256.Sum256(s.Data)}
}

func checkpointTarget(root, rel string) (string, error) {
	if !filepath.IsLocal(rel) || rel == "." {
		return "", fmt.Errorf("invalid checkpoint path: %s", rel)
	}
	path := root
	for _, part := range strings.Split(filepath.Clean(filepath.FromSlash(rel)), string(filepath.Separator)) {
		if strings.EqualFold(part, ".git") {
			return "", fmt.Errorf("checkpoint cannot target Git metadata")
		}
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("checkpoint refuses symlink path: %s", path)
		}
	}
	if safety.IsProtectedPath(path) {
		return "", fmt.Errorf("checkpoint path is protected")
	}
	return path, nil
}

func readCheckpointState(path string) (checkpointState, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return checkpointState{}, nil
	}
	if err != nil {
		return checkpointState{}, err
	}
	if !info.Mode().IsRegular() {
		return checkpointState{}, fmt.Errorf("checkpoint requires a regular file: %s", path)
	}
	if info.Size() > maxCheckpointBytes {
		return checkpointState{}, fmt.Errorf("checkpoint file exceeds %d bytes: %s", maxCheckpointBytes, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return checkpointState{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCheckpointBytes+1))
	if err != nil {
		return checkpointState{}, err
	}
	if len(data) > maxCheckpointBytes {
		return checkpointState{}, fmt.Errorf("checkpoint file exceeds size limit: %s", path)
	}
	return checkpointState{Exists: true, Mode: info.Mode().Perm(), Data: data}, nil
}

func replaceCheckpointFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), "*.checkpoint.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Chmod(mode)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
