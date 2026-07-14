package immutablefile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrExists     = errors.New("immutable destination exists")
	ErrUnsafePath = errors.New("unsafe immutable destination path")
	ErrInvalid    = errors.New("invalid immutable write")
)

type Stage string

const (
	StageValidatePath Stage = "validate-path"
	StageCreateTemp   Stage = "create-temp"
	StageSetMode      Stage = "set-mode"
	StageWrite        Stage = "write"
	StageShortWrite   Stage = "short-write"
	StageSyncFile     Stage = "sync-file"
	StageCloseFile    Stage = "close-file"
	StageInstall      Stage = "install"
	StageSyncInstall  Stage = "sync-install"
	StageRemoveTemp   Stage = "remove-temp"
	StageSyncCleanup  Stage = "sync-cleanup"
)

type Result struct {
	Installed bool
}

type Error struct {
	Stage     Stage
	Installed bool
	Err       error
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("immutable write failed at %s (installed=%t): %v", err.Stage, err.Installed, err.Err)
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

type durableFile interface {
	io.Writer
	Chmod(fs.FileMode) error
	Sync() error
	Close() error
	Name() string
}

type anchoredDirectory interface {
	CreateTemp(string) (durableFile, string, error)
	Lstat(string) (fs.FileInfo, error)
	Link(string, string) error
	Remove(string) error
	Sync() error
	StillAnchored() bool
	Close() error
}

type operations struct {
	lstat      func(string) (fs.FileInfo, error)
	openParent func(string, fs.FileInfo) (anchoredDirectory, error)
}

func defaultOperations() operations {
	return operations{
		lstat: os.Lstat,
		openParent: func(path string, expected fs.FileInfo) (anchoredDirectory, error) {
			root, anchoredInfo, err := openDirectoryChain(path)
			if err != nil {
				return nil, err
			}
			if expected == nil || !os.SameFile(expected, anchoredInfo) {
				_ = root.Close()
				return nil, fmt.Errorf("%w: parent identity changed while anchoring", ErrUnsafePath)
			}
			directory := &osRootDirectory{root: root, path: path, identity: anchoredInfo}
			if !directory.StillAnchored() {
				_ = root.Close()
				return nil, fmt.Errorf("%w: parent changed while it was anchored", ErrUnsafePath)
			}
			return directory, nil
		},
	}
}

type osRootDirectory struct {
	root     *os.Root
	path     string
	identity fs.FileInfo
}

func (directory *osRootDirectory) CreateTemp(prefix string) (durableFile, string, error) {
	for attempt := 0; attempt < 128; attempt++ {
		var entropy [16]byte
		if _, err := io.ReadFull(rand.Reader, entropy[:]); err != nil {
			return nil, "", err
		}
		name := prefix + hex.EncodeToString(entropy[:])
		file, err := directory.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return file, name, nil
	}
	return nil, "", errors.New("temporary filename collision limit exceeded")
}

func (directory *osRootDirectory) Lstat(name string) (fs.FileInfo, error) {
	return directory.root.Lstat(name)
}

func (directory *osRootDirectory) Link(oldName, newName string) error {
	return directory.root.Link(oldName, newName)
}

func (directory *osRootDirectory) Remove(name string) error {
	return directory.root.Remove(name)
}

func (directory *osRootDirectory) Sync() error {
	file, err := directory.root.Open(".")
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (directory *osRootDirectory) StillAnchored() bool {
	pathRoot, current, err := openDirectoryChain(directory.path)
	if err != nil {
		return false
	}
	pathCloseErr := pathRoot.Close()
	if pathCloseErr != nil || !os.SameFile(current, directory.identity) {
		return false
	}
	opened, err := directory.root.Open(".")
	if err != nil {
		return false
	}
	openedInfo, statErr := opened.Stat()
	closeErr := opened.Close()
	return statErr == nil && closeErr == nil && os.SameFile(openedInfo, directory.identity)
}

func (directory *osRootDirectory) Close() error {
	return directory.root.Close()
}

func WriteNoReplace(ctx context.Context, destination string, data []byte) (Result, error) {
	return writeNoReplace(ctx, destination, data, defaultOperations())
}

func WriteNoReplaceExpected(ctx context.Context, destination string, expectedParent fs.FileInfo, data []byte) (Result, error) {
	if expectedParent == nil || !expectedParent.IsDir() {
		return failure(StageValidatePath, false, fmt.Errorf("%w: expected parent identity is invalid", ErrInvalid))
	}
	return writeNoReplaceWithParent(ctx, destination, data, expectedParent, defaultOperations())
}

func writeNoReplace(ctx context.Context, destination string, data []byte, ops operations) (Result, error) {
	return writeNoReplaceWithParent(ctx, destination, data, nil, ops)
}

func writeNoReplaceWithParent(ctx context.Context, destination string, data []byte, requiredParent fs.FileInfo, ops operations) (Result, error) {
	if ctx == nil {
		return failure(StageValidatePath, false, fmt.Errorf("%w: context is nil", ErrInvalid))
	}
	if err := ctx.Err(); err != nil {
		return failure(StageValidatePath, false, err)
	}
	parentPath, targetName, parentInfo, err := validateDestination(destination, ops)
	if err != nil {
		return failure(StageValidatePath, false, err)
	}
	if requiredParent != nil {
		if !os.SameFile(parentInfo, requiredParent) {
			return failure(StageValidatePath, false, fmt.Errorf("%w: destination parent no longer has the required identity", ErrUnsafePath))
		}
		parentInfo = requiredParent
	}
	parent, err := ops.openParent(parentPath, parentInfo)
	if err != nil {
		return failure(StageValidatePath, false, errors.Join(ErrUnsafePath, err))
	}
	defer func() { _ = parent.Close() }()
	if !parent.StillAnchored() {
		return failure(StageValidatePath, false, fmt.Errorf("%w: parent anchor changed", ErrUnsafePath))
	}
	if _, err := parent.Lstat(targetName); err == nil {
		return failure(StageValidatePath, false, ErrExists)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return failure(StageValidatePath, false, fmt.Errorf("%w: cannot inspect destination", ErrUnsafePath))
	}

	temporary, temporaryName, err := parent.CreateTemp("." + targetName + ".tmp-")
	if err != nil {
		return failure(StageCreateTemp, false, err)
	}
	closed := false
	closeTemporary := func() error {
		if !closed {
			err := temporary.Close()
			closed = true
			return err
		}
		return nil
	}
	failBeforeInstall := func(stage Stage, cause error) (Result, error) {
		if closeErr := closeTemporary(); closeErr != nil {
			stage = StageCloseFile
			cause = errors.Join(cause, closeErr)
		}
		return cleanupFailure(parent, temporaryName, false, false, stage, cause)
	}

	if err := ctx.Err(); err != nil {
		return failBeforeInstall(StageSetMode, err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		return failBeforeInstall(StageSetMode, err)
	}
	if err := ctx.Err(); err != nil {
		return failBeforeInstall(StageWrite, err)
	}
	written, err := temporary.Write(data)
	if err != nil {
		return failBeforeInstall(StageWrite, err)
	}
	if written != len(data) {
		return failBeforeInstall(StageShortWrite, io.ErrShortWrite)
	}
	if err := ctx.Err(); err != nil {
		return failBeforeInstall(StageSyncFile, err)
	}
	if err := temporary.Sync(); err != nil {
		return failBeforeInstall(StageSyncFile, err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return cleanupFailure(parent, temporaryName, false, false, StageCloseFile, err)
	}
	closed = true
	if err := ctx.Err(); err != nil {
		return cleanupFailure(parent, temporaryName, false, false, StageInstall, err)
	}
	if !parent.StillAnchored() {
		return cleanupFailure(parent, temporaryName, false, false, StageInstall, fmt.Errorf("%w: parent anchor changed", ErrUnsafePath))
	}
	if err := parent.Link(temporaryName, targetName); err != nil {
		cause := err
		if errors.Is(err, fs.ErrExist) {
			cause = errors.Join(ErrExists, err)
		}
		return cleanupFailure(parent, temporaryName, false, false, StageInstall, cause)
	}
	installed := true
	if !parent.StillAnchored() {
		return cleanupFailure(parent, temporaryName, installed, false, StageSyncInstall, fmt.Errorf("%w: parent anchor changed", ErrUnsafePath))
	}
	if err := ctx.Err(); err != nil {
		return cleanupFailure(parent, temporaryName, installed, false, StageSyncInstall, err)
	}
	if err := parent.Sync(); err != nil {
		return cleanupFailure(parent, temporaryName, installed, false, StageSyncInstall, err)
	}
	if err := parent.Remove(temporaryName); err != nil {
		return cleanupFailure(parent, temporaryName, installed, true, StageRemoveTemp, err)
	}
	contextErr := ctx.Err()
	syncErr := parent.Sync()
	if contextErr == nil {
		contextErr = ctx.Err()
	}
	var completionErr error
	if syncErr != nil {
		completionErr = errors.Join(completionErr, syncErr)
	}
	if contextErr != nil {
		completionErr = errors.Join(completionErr, contextErr)
	}
	if !parent.StillAnchored() {
		completionErr = errors.Join(completionErr, fmt.Errorf("%w: parent anchor changed after cleanup sync", ErrUnsafePath))
	}
	if completionErr != nil {
		return failure(StageSyncCleanup, installed, completionErr)
	}
	return Result{Installed: true}, nil
}

func openDirectoryChain(directory string) (*os.Root, fs.FileInfo, error) {
	if directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, nil, fmt.Errorf("%w: parent directory must be clean and absolute", ErrUnsafePath)
	}
	volume := filepath.VolumeName(directory)
	volumeRoot := volume + string(filepath.Separator)
	remainder := strings.TrimPrefix(directory, volumeRoot)
	components := strings.Split(remainder, string(filepath.Separator))
	if len(components) == 1 && components[0] == "" {
		root, err := os.OpenRoot(volumeRoot)
		if err != nil {
			return nil, nil, err
		}
		opened, err := root.Open(".")
		if err != nil {
			_ = root.Close()
			return nil, nil, err
		}
		info, statErr := opened.Stat()
		closeErr := opened.Close()
		if statErr != nil || closeErr != nil {
			_ = root.Close()
			return nil, nil, fmt.Errorf("cannot inspect volume root")
		}
		return root, info, nil
	}

	current, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return nil, nil, err
	}
	var final fs.FileInfo
	for _, component := range components {
		if component == "" {
			_ = current.Close()
			return nil, nil, fmt.Errorf("%w: empty parent path component", ErrUnsafePath)
		}
		expected, lstatErr := current.Lstat(component)
		if lstatErr != nil || expected.Mode()&fs.ModeSymlink != 0 || !expected.IsDir() {
			_ = current.Close()
			return nil, nil, fmt.Errorf("%w: parent component is not a real directory", ErrUnsafePath)
		}
		child, openErr := current.OpenRoot(component)
		if openErr != nil {
			_ = current.Close()
			return nil, nil, fmt.Errorf("%w: cannot anchor parent component", ErrUnsafePath)
		}
		opened, statErr := child.Open(".")
		if statErr != nil {
			_ = child.Close()
			_ = current.Close()
			return nil, nil, fmt.Errorf("%w: cannot inspect anchored parent component", ErrUnsafePath)
		}
		openedInfo, infoErr := opened.Stat()
		closeFileErr := opened.Close()
		currentInfo, currentErr := current.Lstat(component)
		if infoErr != nil || closeFileErr != nil || currentErr != nil || currentInfo.Mode()&fs.ModeSymlink != 0 || !currentInfo.IsDir() || !openedInfo.IsDir() || !os.SameFile(expected, currentInfo) || !os.SameFile(expected, openedInfo) {
			_ = child.Close()
			_ = current.Close()
			return nil, nil, fmt.Errorf("%w: parent component changed while anchoring", ErrUnsafePath)
		}
		if closeErr := current.Close(); closeErr != nil {
			_ = child.Close()
			return nil, nil, closeErr
		}
		current = child
		final = openedInfo
	}
	return current, final, nil
}

func validateDestination(destination string, ops operations) (string, string, fs.FileInfo, error) {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return "", "", nil, fmt.Errorf("%w: destination must be a clean absolute path", ErrUnsafePath)
	}
	base := filepath.Base(destination)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "", "", nil, fmt.Errorf("%w: destination filename is invalid", ErrUnsafePath)
	}
	parent := filepath.Dir(destination)
	info, err := validateDirectoryChain(parent, ops)
	if err != nil {
		return "", "", nil, err
	}
	return parent, base, info, nil
}

func validateDirectoryChain(directory string, ops operations) (fs.FileInfo, error) {
	volume := filepath.VolumeName(directory)
	remainder := strings.TrimPrefix(directory, volume)
	current := volume + string(filepath.Separator)
	remainder = strings.TrimPrefix(remainder, string(filepath.Separator))
	var final fs.FileInfo
	for _, component := range strings.Split(remainder, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := ops.lstat(current)
		if err != nil {
			return nil, fmt.Errorf("%w: parent component is unavailable", ErrUnsafePath)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("%w: parent component is not a real directory", ErrUnsafePath)
		}
		final = info
	}
	if final == nil {
		info, err := ops.lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: parent directory is invalid", ErrUnsafePath)
		}
		final = info
	}
	return final, nil
}

func cleanupFailure(parent anchoredDirectory, temporaryName string, installed bool, removeAlreadyAttempted bool, originalStage Stage, original error) (Result, error) {
	stage := originalStage
	cause := original
	removeAttempts := 2
	if removeAlreadyAttempted {
		removeAttempts = 1
	}
	for attempt := 0; attempt < removeAttempts; attempt++ {
		removeErr := parent.Remove(temporaryName)
		if removeErr == nil || errors.Is(removeErr, fs.ErrNotExist) {
			break
		}
		stage = StageRemoveTemp
		cause = errors.Join(cause, removeErr)
	}
	if syncErr := parent.Sync(); syncErr != nil {
		stage = StageSyncCleanup
		cause = errors.Join(cause, syncErr)
	}
	return failure(stage, installed, cause)
}

func failure(stage Stage, installed bool, cause error) (Result, error) {
	return Result{Installed: installed}, &Error{Stage: stage, Installed: installed, Err: cause}
}
