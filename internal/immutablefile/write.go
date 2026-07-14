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
	StageValidatePath   Stage = "validate-path"
	StageCreateTemp     Stage = "create-temp"
	StageSetMode        Stage = "set-mode"
	StageWrite          Stage = "write"
	StageShortWrite     Stage = "short-write"
	StageSyncFile       Stage = "sync-file"
	StageCloseFile      Stage = "close-file"
	StageInstall        Stage = "install"
	StageSyncInstall    Stage = "sync-install"
	StageRemoveTemp     Stage = "remove-temp"
	StageSyncCleanup    Stage = "sync-cleanup"
	StageCloseDirectory Stage = "close-directory"
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
	Stat() (fs.FileInfo, error)
	Close() error
	Name() string
}

type anchoredDirectory interface {
	CreateTemp(string) (durableFile, string, error)
	Lstat(string) (fs.FileInfo, error)
	Install(string, string, fs.FileInfo) (bool, error)
	Remove(string) error
	Sync() error
	CheckAnchored() error
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
				identityErr := fmt.Errorf("%w: parent identity changed while anchoring", ErrUnsafePath)
				return nil, errors.Join(identityErr, root.Close())
			}
			directory := &osRootDirectory{root: root, path: path, identity: anchoredInfo}
			if anchorErr := directory.CheckAnchored(); anchorErr != nil {
				return nil, errors.Join(fmt.Errorf("%w: parent changed while it was anchored: %w", ErrUnsafePath, anchorErr), root.Close())
			}
			return directory, nil
		},
	}
}

type osRootDirectory struct {
	root         *os.Root
	path         string
	identity     fs.FileInfo
	openSyncFile func() (directorySyncFile, error)
	linkNames    func(string, string) error
}

type directorySyncFile interface {
	Sync() error
	Close() error
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

func (directory *osRootDirectory) Install(oldName, newName string, expected fs.FileInfo) (bool, error) {
	current, err := directory.root.Lstat(oldName)
	if err != nil {
		return false, fmt.Errorf("%w: inspect temporary name before install: %w", ErrUnsafePath, err)
	}
	if !sameFileSnapshot(expected, current) {
		return false, fmt.Errorf("%w: temporary name identity changed before install", ErrUnsafePath)
	}
	linkNames := directory.linkNames
	if linkNames == nil {
		linkNames = directory.root.Link
	}
	linkErr := linkNames(oldName, newName)
	target, targetErr := directory.root.Lstat(newName)
	if linkErr == nil {
		if targetErr != nil {
			return true, fmt.Errorf("%w: inspect installed target: %w", ErrUnsafePath, targetErr)
		}
		if !sameFileSnapshot(expected, target) {
			return true, fmt.Errorf("%w: installed target identity differs from synchronized temporary file", ErrUnsafePath)
		}
		return true, nil
	}
	if targetErr == nil && sameFileSnapshot(expected, target) {
		return true, linkErr
	}
	if targetErr != nil && !errors.Is(targetErr, fs.ErrNotExist) {
		return false, errors.Join(linkErr, fmt.Errorf("%w: reconcile failed install target: %w", ErrUnsafePath, targetErr))
	}
	return false, linkErr
}

func (directory *osRootDirectory) Remove(name string) error {
	return directory.root.Remove(name)
}

func (directory *osRootDirectory) Sync() error {
	openSyncFile := directory.openSyncFile
	if openSyncFile == nil {
		openSyncFile = func() (directorySyncFile, error) {
			return directory.root.Open(".")
		}
	}
	file, err := openSyncFile()
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(syncErr, closeErr)
}

func (directory *osRootDirectory) CheckAnchored() error {
	pathRoot, current, err := openDirectoryChain(directory.path)
	if err != nil {
		return err
	}
	pathCloseErr := pathRoot.Close()
	if pathCloseErr != nil {
		return fmt.Errorf("close re-opened parent anchor: %w", pathCloseErr)
	}
	if !os.SameFile(current, directory.identity) {
		return fmt.Errorf("%w: parent path identity changed", ErrUnsafePath)
	}
	opened, err := directory.root.Open(".")
	if err != nil {
		return fmt.Errorf("open anchored parent: %w", err)
	}
	openedInfo, statErr := opened.Stat()
	closeErr := opened.Close()
	if statErr != nil || closeErr != nil {
		return errors.Join(statErr, closeErr)
	}
	if !os.SameFile(openedInfo, directory.identity) {
		return fmt.Errorf("%w: open parent anchor identity changed", ErrUnsafePath)
	}
	return nil
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

func writeNoReplaceWithParent(ctx context.Context, destination string, data []byte, requiredParent fs.FileInfo, ops operations) (result Result, resultErr error) {
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
	defer func() {
		closeErr := parent.Close()
		if closeErr == nil {
			return
		}
		if resultErr == nil {
			resultErr = &Error{Stage: StageCloseDirectory, Installed: result.Installed, Err: closeErr}
			return
		}
		var writeErr *Error
		if errors.As(resultErr, &writeErr) {
			writeErr.Err = errors.Join(writeErr.Err, closeErr)
			return
		}
		resultErr = errors.Join(resultErr, closeErr)
	}()
	if err := parent.CheckAnchored(); err != nil {
		return failure(StageValidatePath, false, fmt.Errorf("%w: parent anchor changed: %w", ErrUnsafePath, err))
	}
	if _, err := parent.Lstat(targetName); err == nil {
		return failure(StageValidatePath, false, ErrExists)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return failure(StageValidatePath, false, fmt.Errorf("%w: cannot inspect destination: %w", ErrUnsafePath, err))
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
	temporaryInfo, err := temporary.Stat()
	if err != nil {
		return failBeforeInstall(StageInstall, err)
	}
	if !temporaryInfo.Mode().IsRegular() {
		return failBeforeInstall(StageInstall, fmt.Errorf("%w: synchronized temporary file is not regular", ErrUnsafePath))
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return cleanupFailure(parent, temporaryName, false, false, StageCloseFile, err)
	}
	closed = true
	if err := ctx.Err(); err != nil {
		return cleanupFailure(parent, temporaryName, false, false, StageInstall, err)
	}
	if err := parent.CheckAnchored(); err != nil {
		return cleanupFailure(parent, temporaryName, false, false, StageInstall, fmt.Errorf("%w: parent anchor changed: %w", ErrUnsafePath, err))
	}
	installed, installErr := parent.Install(temporaryName, targetName, temporaryInfo)
	if !installed {
		cause := installErr
		if cause == nil {
			cause = fmt.Errorf("%w: install returned neither a target nor an error", ErrUnsafePath)
		}
		if errors.Is(cause, fs.ErrExist) {
			cause = errors.Join(ErrExists, cause)
		}
		return cleanupFailure(parent, temporaryName, false, false, StageInstall, cause)
	}
	if err := parent.CheckAnchored(); err != nil {
		return cleanupFailure(parent, temporaryName, installed, false, StageSyncInstall, errors.Join(installErr, fmt.Errorf("%w: parent anchor changed: %w", ErrUnsafePath, err)))
	}
	if err := ctx.Err(); err != nil {
		return cleanupFailure(parent, temporaryName, installed, false, StageSyncInstall, errors.Join(installErr, err))
	}
	if err := parent.Sync(); err != nil {
		return cleanupFailure(parent, temporaryName, installed, false, StageSyncInstall, errors.Join(installErr, err))
	}
	if err := parent.Remove(temporaryName); err != nil {
		return cleanupFailure(parent, temporaryName, installed, true, StageRemoveTemp, errors.Join(installErr, err))
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
	if err := parent.CheckAnchored(); err != nil {
		completionErr = errors.Join(completionErr, fmt.Errorf("%w: parent anchor changed after cleanup sync: %w", ErrUnsafePath, err))
	}
	if completionErr != nil {
		return failure(StageSyncCleanup, installed, errors.Join(installErr, completionErr))
	}
	if installErr != nil {
		return failure(StageInstall, installed, installErr)
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
			return nil, nil, fmt.Errorf("%w: open volume root: %w", ErrUnsafePath, err)
		}
		opened, err := root.Open(".")
		if err != nil {
			return nil, nil, closeRootErrors(fmt.Errorf("%w: open volume root directory: %w", ErrUnsafePath, err), root)
		}
		info, statErr := opened.Stat()
		closeErr := opened.Close()
		if statErr != nil || closeErr != nil {
			inspectionErr := errors.Join(
				wrapCause(ErrUnsafePath, "stat volume root directory", statErr),
				wrapCause(ErrUnsafePath, "close volume root inspection", closeErr),
			)
			return nil, nil, closeRootErrors(inspectionErr, root)
		}
		return root, info, nil
	}

	current, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: open volume root: %w", ErrUnsafePath, err)
	}
	var final fs.FileInfo
	for _, component := range components {
		if component == "" {
			return nil, nil, closeRootErrors(fmt.Errorf("%w: empty parent path component", ErrUnsafePath), current)
		}
		expected, lstatErr := current.Lstat(component)
		if lstatErr != nil {
			return nil, nil, closeRootErrors(fmt.Errorf("%w: inspect parent component %q: %w", ErrUnsafePath, component, lstatErr), current)
		}
		if expected.Mode()&fs.ModeSymlink != 0 || !expected.IsDir() {
			return nil, nil, closeRootErrors(fmt.Errorf("%w: parent component %q is not a real directory", ErrUnsafePath, component), current)
		}
		child, openErr := current.OpenRoot(component)
		if openErr != nil {
			return nil, nil, closeRootErrors(fmt.Errorf("%w: anchor parent component %q: %w", ErrUnsafePath, component, openErr), current)
		}
		opened, statErr := child.Open(".")
		if statErr != nil {
			return nil, nil, closeRootErrors(fmt.Errorf("%w: open anchored parent component %q: %w", ErrUnsafePath, component, statErr), child, current)
		}
		openedInfo, infoErr := opened.Stat()
		closeFileErr := opened.Close()
		currentInfo, currentErr := current.Lstat(component)
		inspectionErr := errors.Join(
			wrapCause(ErrUnsafePath, "stat anchored parent component "+component, infoErr),
			wrapCause(ErrUnsafePath, "close anchored parent component inspection "+component, closeFileErr),
			wrapCause(ErrUnsafePath, "re-inspect parent component "+component, currentErr),
		)
		if inspectionErr != nil {
			return nil, nil, closeRootErrors(inspectionErr, child, current)
		}
		if currentInfo.Mode()&fs.ModeSymlink != 0 || !currentInfo.IsDir() || !openedInfo.IsDir() || !os.SameFile(expected, currentInfo) || !os.SameFile(expected, openedInfo) {
			return nil, nil, closeRootErrors(fmt.Errorf("%w: parent component %q changed while anchoring", ErrUnsafePath, component), child, current)
		}
		if closeErr := current.Close(); closeErr != nil {
			return nil, nil, closeRootErrors(fmt.Errorf("%w: close parent anchor before descending into %q: %w", ErrUnsafePath, component, closeErr), child)
		}
		current = child
		final = openedInfo
	}
	return current, final, nil
}

func wrapCause(category error, operation string, cause error) error {
	if cause == nil {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", category, operation, cause)
}

func closeRootErrors(primary error, roots ...*os.Root) error {
	causes := []error{primary}
	for _, root := range roots {
		if root != nil {
			causes = append(causes, root.Close())
		}
	}
	return errors.Join(causes...)
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
			return nil, fmt.Errorf("%w: parent component is unavailable: %w", ErrUnsafePath, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("%w: parent component is not a real directory", ErrUnsafePath)
		}
		final = info
	}
	if final == nil {
		info, err := ops.lstat(current)
		if err != nil {
			return nil, fmt.Errorf("%w: parent directory is unavailable: %w", ErrUnsafePath, err)
		}
		if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
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

func sameFileSnapshot(left, right fs.FileInfo) bool {
	return left != nil && right != nil && left.Mode().IsRegular() && right.Mode().IsRegular() &&
		os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}
