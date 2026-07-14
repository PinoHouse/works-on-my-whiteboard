package immutablefile

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWriteNoReplaceInstallsDurablyWithoutTempLeak(t *testing.T) {
	directory := safeTempDir(t)
	target := filepath.Join(directory, "record.json")
	result, err := WriteNoReplace(context.Background(), target, []byte("payload\n"))
	if err != nil {
		t.Fatalf("WriteNoReplace: %v", err)
	}
	if !result.Installed {
		t.Fatal("successful result did not report Installed")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "payload\n" {
		t.Fatalf("target = %q, %v", data, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("target mode = %v, %v", info.Mode(), err)
	}
	assertNoTemps(t, directory)
}

func TestWriteNoReplaceNeverReplacesExistingTargetOfAnyKind(t *testing.T) {
	tests := []struct {
		name   string
		create func(*testing.T, string)
	}{
		{name: "regular", create: func(t *testing.T, path string) { writeFixtureFile(t, path, []byte("old")) }},
		{name: "directory", create: func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}},
		{name: "symlink", create: func(t *testing.T, path string) {
			other := path + ".other"
			writeFixtureFile(t, other, []byte("other"))
			if err := os.Symlink(other, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := safeTempDir(t)
			target := filepath.Join(directory, "target")
			test.create(t, target)
			result, err := WriteNoReplace(context.Background(), target, []byte("new"))
			if !errors.Is(err, ErrExists) || result.Installed {
				t.Fatalf("result=%+v error=%v, want non-installed ErrExists", result, err)
			}
			assertNoTemps(t, directory)
		})
	}
}

func TestWriteNoReplaceRejectsUnsafePathsBeforeCreatingTemp(t *testing.T) {
	directory := safeTempDir(t)
	realParent := filepath.Join(directory, "real")
	if err := os.Mkdir(realParent, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	symlinkParent := filepath.Join(directory, "alias")
	if err := os.Symlink(realParent, symlinkParent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	for _, target := range []string{
		filepath.Join(symlinkParent, "target"),
		filepath.Join(directory, "missing", "target"),
		"relative/target",
		directory + string(filepath.Separator) + "." + string(filepath.Separator) + "target",
	} {
		result, err := WriteNoReplace(context.Background(), target, []byte("x"))
		if !errors.Is(err, ErrUnsafePath) || result.Installed {
			t.Fatalf("target %q result=%+v error=%v, want ErrUnsafePath", target, result, err)
		}
	}
	assertNoTemps(t, realParent)
}

func TestWriteNoReplacePreservesPathInspectionCauses(t *testing.T) {
	t.Run("parent chain lstat", func(t *testing.T) {
		directory := safeTempDir(t)
		target := filepath.Join(directory, "target")
		injected := errors.New("injected parent chain lstat failure")
		ops := defaultOperations()
		ops.lstat = func(string) (fs.FileInfo, error) {
			return nil, injected
		}
		result, err := writeNoReplace(context.Background(), target, []byte("payload"), ops)
		var writeErr *Error
		if !errors.As(err, &writeErr) || writeErr.Stage != StageValidatePath || result.Installed || !errors.Is(err, ErrUnsafePath) || !errors.Is(err, injected) {
			t.Fatalf("result=%+v error=%v, want validate-path unsafe preserving lstat cause", result, err)
		}
	})

	t.Run("destination lstat", func(t *testing.T) {
		directory := safeTempDir(t)
		target := filepath.Join(directory, "target")
		injected := errors.New("injected destination lstat failure")
		ops := defaultOperations()
		originalOpen := ops.openParent
		ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
			parent, err := originalOpen(path, info)
			if err != nil {
				return nil, err
			}
			return &lstatFailureDirectory{anchoredDirectory: parent, injected: injected}, nil
		}
		result, err := writeNoReplace(context.Background(), target, []byte("payload"), ops)
		var writeErr *Error
		if !errors.As(err, &writeErr) || writeErr.Stage != StageValidatePath || result.Installed || !errors.Is(err, ErrUnsafePath) || !errors.Is(err, injected) {
			t.Fatalf("result=%+v error=%v, want validate-path unsafe preserving destination cause", result, err)
		}
	})
}

func TestOSRootDirectorySyncPreservesEveryOperationalCause(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		injected := errors.New("injected directory open failure")
		directory := &osRootDirectory{
			openSyncFile: func() (directorySyncFile, error) {
				return nil, injected
			},
		}
		if err := directory.Sync(); !errors.Is(err, injected) {
			t.Fatalf("Sync error = %v, want open cause", err)
		}
	})

	t.Run("sync and close", func(t *testing.T) {
		syncFailure := errors.New("injected directory sync failure")
		closeFailure := errors.New("injected directory close failure")
		directory := &osRootDirectory{
			openSyncFile: func() (directorySyncFile, error) {
				return &syncCloseFailureFile{syncFailure: syncFailure, closeFailure: closeFailure}, nil
			},
		}
		err := directory.Sync()
		if !errors.Is(err, syncFailure) || !errors.Is(err, closeFailure) {
			t.Fatalf("Sync error lost a cause: %v", err)
		}
	})
}

type syncCloseFailureFile struct {
	syncFailure  error
	closeFailure error
}

func (file *syncCloseFailureFile) Sync() error {
	return file.syncFailure
}

func (file *syncCloseFailureFile) Close() error {
	return file.closeFailure
}

type lstatFailureDirectory struct {
	anchoredDirectory
	injected error
}

func (directory *lstatFailureDirectory) Lstat(string) (fs.FileInfo, error) {
	return nil, directory.injected
}

func TestWriteNoReplaceIndependentWritersHaveExactlyOneWinner(t *testing.T) {
	directory := safeTempDir(t)
	target := filepath.Join(directory, "winner")
	const writers = 32
	var installed atomic.Int64
	var conflicts atomic.Int64
	var unexpected atomic.Value
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(value byte) {
			defer wait.Done()
			<-start
			result, err := WriteNoReplace(context.Background(), target, []byte{value})
			switch {
			case err == nil && result.Installed:
				installed.Add(1)
			case errors.Is(err, ErrExists) && !result.Installed:
				conflicts.Add(1)
			default:
				unexpected.Store(errors.New("unexpected writer result"))
			}
		}(byte(index))
	}
	close(start)
	wait.Wait()
	if value := unexpected.Load(); value != nil {
		t.Fatal(value)
	}
	if installed.Load() != 1 || conflicts.Load() != writers-1 {
		t.Fatalf("installed=%d conflicts=%d", installed.Load(), conflicts.Load())
	}
	assertNoTemps(t, directory)
}

func TestWriteNoReplaceReportsEveryDurabilityStage(t *testing.T) {
	tests := []struct {
		name          string
		stage         Stage
		wantInstalled bool
	}{
		{name: "create", stage: StageCreateTemp},
		{name: "chmod", stage: StageSetMode},
		{name: "write", stage: StageWrite},
		{name: "short write", stage: StageShortWrite},
		{name: "file sync", stage: StageSyncFile},
		{name: "close", stage: StageCloseFile},
		{name: "install", stage: StageInstall},
		{name: "first directory sync", stage: StageSyncInstall, wantInstalled: true},
		{name: "remove temp", stage: StageRemoveTemp, wantInstalled: true},
		{name: "cleanup directory sync", stage: StageSyncCleanup, wantInstalled: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := safeTempDir(t)
			target := filepath.Join(directory, "target")
			injected := errors.New("injected " + test.name)
			ops := newFaultOperations(test.stage, injected)
			result, err := writeNoReplace(context.Background(), target, []byte("payload"), ops.operations())
			var writeErr *Error
			if !errors.As(err, &writeErr) {
				t.Fatalf("error = %T %v, want *Error", err, err)
			}
			if writeErr.Stage != test.stage || writeErr.Installed != test.wantInstalled || result.Installed != test.wantInstalled {
				t.Fatalf("result=%+v write error=%+v", result, writeErr)
			}
			if !errors.Is(err, injected) && !(test.stage == StageShortWrite && errors.Is(err, io.ErrShortWrite)) {
				t.Fatalf("error %v does not unwrap injected/short-write error", err)
			}
			if test.wantInstalled {
				if _, statErr := os.Lstat(target); statErr != nil {
					t.Fatalf("installed target missing: %v", statErr)
				}
			} else if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("pre-install failure left target: %v", statErr)
			}
			if test.stage != StageRemoveTemp {
				assertNoTemps(t, directory)
			}
		})
	}
}

func TestWriteNoReplaceReportsParentCloseFailureAfterSuccess(t *testing.T) {
	directory := safeTempDir(t)
	target := filepath.Join(directory, "target")
	closeFailure := errors.New("injected parent close failure")
	var wrapped *closeFailureDirectory
	ops := defaultOperations()
	originalOpen := ops.openParent
	ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
		parent, err := originalOpen(path, info)
		if err != nil {
			return nil, err
		}
		wrapped = &closeFailureDirectory{anchoredDirectory: parent, injected: closeFailure}
		return wrapped, nil
	}

	result, err := writeNoReplace(context.Background(), target, []byte("payload"), ops)
	var writeErr *Error
	if !errors.As(err, &writeErr) || writeErr.Stage != StageCloseDirectory || !writeErr.Installed || !result.Installed || !errors.Is(err, closeFailure) {
		t.Fatalf("result=%+v error=%v, want installed close-directory failure", result, err)
	}
	if wrapped == nil || wrapped.closeCalls != 1 {
		t.Fatalf("parent close calls=%d, want exactly one", wrapped.closeCalls)
	}
	if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "payload" {
		t.Fatalf("installed target = %q, %v", data, readErr)
	}
	assertNoTemps(t, directory)
}

func TestWriteNoReplacePreservesPrimaryFailureWhenParentCloseAlsoFails(t *testing.T) {
	tests := []struct {
		name          string
		stage         Stage
		wantInstalled bool
	}{
		{name: "before install", stage: StageWrite},
		{name: "after install", stage: StageSyncInstall, wantInstalled: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := safeTempDir(t)
			target := filepath.Join(directory, "target")
			primary := errors.New("injected primary failure")
			closeFailure := errors.New("injected parent close failure")
			fault := newFaultOperations(test.stage, primary)
			ops := fault.operations()
			originalOpen := ops.openParent
			var wrapped *closeFailureDirectory
			ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
				parent, err := originalOpen(path, info)
				if err != nil {
					return nil, err
				}
				wrapped = &closeFailureDirectory{anchoredDirectory: parent, injected: closeFailure}
				return wrapped, nil
			}

			result, err := writeNoReplace(context.Background(), target, []byte("payload"), ops)
			var writeErr *Error
			if !errors.As(err, &writeErr) || writeErr.Stage != test.stage || writeErr.Installed != test.wantInstalled || result.Installed != test.wantInstalled {
				t.Fatalf("result=%+v error=%v, want original stage %s installed=%t", result, err, test.stage, test.wantInstalled)
			}
			if !errors.Is(err, primary) || !errors.Is(err, closeFailure) {
				t.Fatalf("combined failure lost cause: %v", err)
			}
			if wrapped == nil || wrapped.closeCalls != 1 {
				t.Fatalf("parent close calls=%d, want exactly one", wrapped.closeCalls)
			}
		})
	}
}

type closeFailureDirectory struct {
	anchoredDirectory
	injected   error
	closeCalls int
}

func (directory *closeFailureDirectory) Close() error {
	directory.closeCalls++
	realCloseErr := directory.anchoredDirectory.Close()
	return errors.Join(realCloseErr, directory.injected)
}

func TestWriteNoReplaceReportsCleanupFailureWithoutLosingOriginalCause(t *testing.T) {
	tests := []struct {
		name          string
		originalStage Stage
		cleanupStage  Stage
		wantInstalled bool
		wantTempLeak  bool
	}{
		{name: "preinstall remove", originalStage: StageWrite, cleanupStage: StageRemoveTemp, wantTempLeak: true},
		{name: "preinstall close", originalStage: StageWrite, cleanupStage: StageCloseFile},
		{name: "preinstall directory sync", originalStage: StageWrite, cleanupStage: StageSyncCleanup},
		{name: "postinstall directory sync", originalStage: StageSyncInstall, cleanupStage: StageSyncCleanup, wantInstalled: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := safeTempDir(t)
			target := filepath.Join(directory, "target")
			original := errors.New("original failure")
			cleanup := errors.New("cleanup failure")
			fault := newFaultOperations(test.originalStage, original)
			fault.cleanupStage = test.cleanupStage
			fault.cleanupInjected = cleanup
			result, err := writeNoReplace(context.Background(), target, []byte("payload"), fault.operations())
			var writeErr *Error
			if !errors.As(err, &writeErr) || writeErr.Stage != test.cleanupStage || writeErr.Installed != test.wantInstalled || result.Installed != test.wantInstalled {
				t.Fatalf("result=%+v error=%#v", result, writeErr)
			}
			if !errors.Is(err, original) || !errors.Is(err, cleanup) {
				t.Fatalf("cleanup error lost a cause: %v", err)
			}
			if test.wantInstalled {
				if _, statErr := os.Lstat(target); statErr != nil {
					t.Fatalf("installed target missing: %v", statErr)
				}
			} else if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("preinstall failure left target: %v", statErr)
			}
			if !test.wantTempLeak {
				assertNoTemps(t, directory)
			}
		})
	}
}

func TestWriteNoReplaceRetriesPostInstallTempRemovalAndSyncsCleanup(t *testing.T) {
	directory := safeTempDir(t)
	target := filepath.Join(directory, "target")
	injected := errors.New("one-shot remove failure")
	var wrapped *oneShotRemoveDirectory
	ops := defaultOperations()
	originalOpen := ops.openParent
	ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
		parent, err := originalOpen(path, info)
		if err != nil {
			return nil, err
		}
		wrapped = &oneShotRemoveDirectory{anchoredDirectory: parent, injected: injected}
		return wrapped, nil
	}
	result, err := writeNoReplace(context.Background(), target, []byte("payload"), ops)
	var writeErr *Error
	if !errors.As(err, &writeErr) || !errors.Is(err, injected) || writeErr.Stage != StageRemoveTemp || !writeErr.Installed || !result.Installed {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if wrapped == nil || wrapped.removeCalls != 2 || wrapped.syncCalls < 2 {
		t.Fatalf("remove calls=%d sync calls=%d, want retry and cleanup sync", wrapped.removeCalls, wrapped.syncCalls)
	}
	assertNoTemps(t, directory)
}

func TestWriteNoReplaceCleanupStageUsesLastDurabilityFailure(t *testing.T) {
	directory := safeTempDir(t)
	target := filepath.Join(directory, "target")
	original := errors.New("write failure")
	removeFailure := errors.New("persistent remove failure")
	syncFailure := errors.New("cleanup sync failure")
	fault := newFaultOperations(StageWrite, original)
	fault.cleanupStage = StageRemoveTemp
	fault.cleanupInjected = removeFailure
	fault.cleanupSyncInjected = syncFailure
	result, err := writeNoReplace(context.Background(), target, []byte("payload"), fault.operations())
	var writeErr *Error
	if !errors.As(err, &writeErr) || writeErr.Stage != StageSyncCleanup || writeErr.Installed || result.Installed {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if !errors.Is(err, original) || !errors.Is(err, removeFailure) || !errors.Is(err, syncFailure) {
		t.Fatalf("cleanup priority lost a cause: %v", err)
	}
	if fault.closeCalls != 1 || fault.syncCalls != 1 {
		t.Fatalf("close calls=%d sync calls=%d, want exactly one each", fault.closeCalls, fault.syncCalls)
	}
}

type oneShotRemoveDirectory struct {
	anchoredDirectory
	injected    error
	removeCalls int
	syncCalls   int
}

func (directory *oneShotRemoveDirectory) Remove(name string) error {
	directory.removeCalls++
	if directory.removeCalls == 1 {
		return directory.injected
	}
	return directory.anchoredDirectory.Remove(name)
}

func (directory *oneShotRemoveDirectory) Sync() error {
	directory.syncCalls++
	return directory.anchoredDirectory.Sync()
}

func TestWriteNoReplaceCancellationPreservesInstalledClassification(t *testing.T) {
	directory := safeTempDir(t)
	target := filepath.Join(directory, "target")
	ctx, cancel := context.WithCancel(context.Background())
	ops := defaultOperations()
	originalOpen := ops.openParent
	ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
		directory, openErr := originalOpen(path, info)
		if openErr != nil {
			return nil, openErr
		}
		return &cancelAfterLinkDirectory{anchoredDirectory: directory, cancel: cancel}, nil
	}
	result, err := writeNoReplace(ctx, target, []byte("payload"), ops)
	var writeErr *Error
	if !errors.As(err, &writeErr) || !errors.Is(err, context.Canceled) || !result.Installed || !writeErr.Installed {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("installed target missing: %v", statErr)
	}
	assertNoTemps(t, directory)
}

func TestWriteNoReplaceRejectsTemporaryNameReplacementDuringLink(t *testing.T) {
	directory := safeTempDir(t)
	target := filepath.Join(directory, "target")
	ops := defaultOperations()
	originalOpen := ops.openParent
	ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
		parent, err := originalOpen(path, info)
		if err != nil {
			return nil, err
		}
		return &replaceTempBeforeLinkDirectory{anchoredDirectory: parent, path: path}, nil
	}

	result, err := writeNoReplace(context.Background(), target, []byte("trusted payload"), ops)
	var writeErr *Error
	if !errors.As(err, &writeErr) || !errors.Is(err, ErrUnsafePath) || writeErr.Installed || result.Installed {
		t.Fatalf("result=%+v error=%v, want pre-install fail-closed ErrUnsafePath", result, err)
	}
	if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("temporary-name replacement created target: %v", statErr)
	}
	assertNoTemps(t, directory)
}

type replaceTempBeforeLinkDirectory struct {
	anchoredDirectory
	path string
}

func (directory *replaceTempBeforeLinkDirectory) Install(oldName, newName string, expected fs.FileInfo) (bool, error) {
	temporaryPath := filepath.Join(directory.path, oldName)
	if err := os.Remove(temporaryPath); err != nil {
		return false, err
	}
	if err := os.WriteFile(temporaryPath, []byte("attacker payload"), 0o644); err != nil {
		return false, err
	}
	return directory.anchoredDirectory.Install(oldName, newName, expected)
}

func TestWriteNoReplaceReconcilesLinkThatSucceededBeforeReturningError(t *testing.T) {
	directory := safeTempDir(t)
	target := filepath.Join(directory, "target")
	injected := errors.New("injected ambiguous link failure")
	var wrapped *linkThenErrorDirectory
	ops := defaultOperations()
	originalOpen := ops.openParent
	ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
		parent, err := originalOpen(path, info)
		if err != nil {
			return nil, err
		}
		wrapped = &linkThenErrorDirectory{anchoredDirectory: parent, injected: injected}
		return wrapped, nil
	}

	result, err := writeNoReplace(context.Background(), target, []byte("payload"), ops)
	var writeErr *Error
	if !errors.As(err, &writeErr) || writeErr.Stage != StageInstall || !errors.Is(err, injected) || !writeErr.Installed || !result.Installed {
		t.Fatalf("result=%+v error=%v, want installed StageInstall preserving ambiguous link cause", result, err)
	}
	if wrapped == nil || wrapped.syncCalls != 2 {
		t.Fatalf("directory sync calls=%d, want both durability syncs", wrapped.syncCalls)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != "payload" {
		t.Fatalf("installed target = %q, %v", data, readErr)
	}
	assertNoTemps(t, directory)
}

func TestOSRootDirectoryInstallReconcilesLinkOutcomeByIdentity(t *testing.T) {
	tests := []struct {
		name      string
		link      func(*osRootDirectory, string, string, error) error
		wantCause error
		wantBytes string
	}{
		{
			name: "link succeeded before error",
			link: func(directory *osRootDirectory, oldName, newName string, injected error) error {
				if err := directory.root.Link(oldName, newName); err != nil {
					return err
				}
				return injected
			},
			wantBytes: "payload",
		},
		{
			name: "target replaced after link",
			link: func(directory *osRootDirectory, oldName, newName string, _ error) error {
				if err := directory.root.Link(oldName, newName); err != nil {
					return err
				}
				if err := directory.root.Remove(newName); err != nil {
					return err
				}
				file, err := directory.root.OpenFile(newName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
				if err != nil {
					return err
				}
				if _, err := file.Write([]byte("attacker")); err != nil {
					_ = file.Close()
					return err
				}
				return file.Close()
			},
			wantCause: ErrUnsafePath,
			wantBytes: "attacker",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := safeTempDir(t)
			temporaryName := ".target.tmp-test"
			targetName := "target"
			temporaryPath := filepath.Join(path, temporaryName)
			writeFixtureFile(t, temporaryPath, []byte("payload"))
			expected, err := os.Lstat(temporaryPath)
			if err != nil {
				t.Fatalf("lstat temporary: %v", err)
			}
			root, identity, err := openDirectoryChain(path)
			if err != nil {
				t.Fatalf("open directory chain: %v", err)
			}
			directory := &osRootDirectory{root: root, path: path, identity: identity}
			t.Cleanup(func() { _ = directory.Close() })
			injected := errors.New("injected ambiguous link error")
			directory.linkNames = func(oldName, newName string) error {
				return test.link(directory, oldName, newName, injected)
			}

			installed, installErr := directory.Install(temporaryName, targetName, expected)
			if !installed {
				t.Fatalf("installed=false error=%v, want target linearization", installErr)
			}
			if test.wantCause == nil {
				if !errors.Is(installErr, injected) {
					t.Fatalf("Install error = %v, want ambiguous link cause", installErr)
				}
			} else if !errors.Is(installErr, test.wantCause) {
				t.Fatalf("Install error = %v, want %v", installErr, test.wantCause)
			}
			data, readErr := os.ReadFile(filepath.Join(path, targetName))
			if readErr != nil || string(data) != test.wantBytes {
				t.Fatalf("retained target = %q, %v", data, readErr)
			}
		})
	}
}

type linkThenErrorDirectory struct {
	anchoredDirectory
	injected  error
	syncCalls int
}

func (directory *linkThenErrorDirectory) Install(oldName, newName string, expected fs.FileInfo) (bool, error) {
	installed, err := directory.anchoredDirectory.Install(oldName, newName, expected)
	if err != nil {
		return installed, err
	}
	return installed, directory.injected
}

func (directory *linkThenErrorDirectory) Sync() error {
	directory.syncCalls++
	return directory.anchoredDirectory.Sync()
}

func TestWriteNoReplaceCancellationAfterTempRemovalStillSyncsCleanup(t *testing.T) {
	directory := safeTempDir(t)
	target := filepath.Join(directory, "target")
	ctx, cancel := context.WithCancel(context.Background())
	var wrapped *cancelAfterRemoveDirectory
	ops := defaultOperations()
	originalOpen := ops.openParent
	ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
		parent, err := originalOpen(path, info)
		if err != nil {
			return nil, err
		}
		wrapped = &cancelAfterRemoveDirectory{anchoredDirectory: parent, cancel: cancel}
		return wrapped, nil
	}
	result, err := writeNoReplace(ctx, target, []byte("payload"), ops)
	var writeErr *Error
	if !errors.As(err, &writeErr) || !errors.Is(err, context.Canceled) || writeErr.Stage != StageSyncCleanup || !writeErr.Installed || !result.Installed {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if wrapped == nil || wrapped.syncCalls != 2 {
		t.Fatalf("cleanup sync calls=%d, want 2 total directory syncs", wrapped.syncCalls)
	}
	assertNoTemps(t, directory)
}

type cancelAfterRemoveDirectory struct {
	anchoredDirectory
	cancel    context.CancelFunc
	syncCalls int
}

func (directory *cancelAfterRemoveDirectory) Remove(name string) error {
	if err := directory.anchoredDirectory.Remove(name); err != nil {
		return err
	}
	directory.cancel()
	return nil
}

func (directory *cancelAfterRemoveDirectory) Sync() error {
	directory.syncCalls++
	return directory.anchoredDirectory.Sync()
}

type cancelAfterLinkDirectory struct {
	anchoredDirectory
	cancel context.CancelFunc
}

func (directory *cancelAfterLinkDirectory) Install(oldName, newName string, expected fs.FileInfo) (bool, error) {
	installed, err := directory.anchoredDirectory.Install(oldName, newName, expected)
	if err != nil {
		return installed, err
	}
	directory.cancel()
	return installed, nil
}

func TestWriteNoReplaceAnchorsParentAgainstSymlinkSwap(t *testing.T) {
	root := safeTempDir(t)
	parent := filepath.Join(root, "parent")
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	target := filepath.Join(parent, "target")
	ops := defaultOperations()
	originalOpen := ops.openParent
	ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
		directory, openErr := originalOpen(path, info)
		if openErr != nil {
			return nil, openErr
		}
		if err := os.Rename(parent, parent+".moved"); err != nil {
			_ = directory.Close()
			return nil, err
		}
		if err := os.Symlink(outside, parent); err != nil {
			_ = directory.Close()
			return nil, err
		}
		return directory, nil
	}
	result, err := writeNoReplace(context.Background(), target, []byte("payload"), ops)
	if !errors.Is(err, ErrUnsafePath) || result.Installed {
		t.Fatalf("result=%+v error=%v, want non-installed ErrUnsafePath", result, err)
	}
	if _, statErr := os.Lstat(filepath.Join(outside, "target")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("parent swap redirected installation outside: %v", statErr)
	}
}

func TestWriteNoReplaceRejectsAncestorSymlinkRestoredToSameParentInode(t *testing.T) {
	root := safeTempDir(t)
	ancestor := filepath.Join(root, "ancestor")
	parent := filepath.Join(ancestor, "parent")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	target := filepath.Join(parent, "target")
	ops := defaultOperations()
	originalOpen := ops.openParent
	ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
		directory, openErr := originalOpen(path, info)
		if openErr != nil {
			return nil, openErr
		}
		moved := ancestor + ".real"
		if err := os.Rename(ancestor, moved); err != nil {
			_ = directory.Close()
			return nil, err
		}
		if err := os.Symlink(moved, ancestor); err != nil {
			_ = directory.Close()
			return nil, err
		}
		return directory, nil
	}
	result, err := writeNoReplace(context.Background(), target, []byte("payload"), ops)
	if !errors.Is(err, ErrUnsafePath) || result.Installed {
		t.Fatalf("result=%+v error=%v, want non-installed ErrUnsafePath", result, err)
	}
	if _, statErr := os.Lstat(filepath.Join(ancestor+".real", "parent", "target")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("ancestor alias allowed installation: %v", statErr)
	}
}

func TestWriteNoReplaceRevalidatesAnchorAfterFinalDirectorySync(t *testing.T) {
	root := safeTempDir(t)
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	target := filepath.Join(parent, "target")
	ops := defaultOperations()
	originalOpen := ops.openParent
	ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
		directory, err := originalOpen(path, info)
		if err != nil {
			return nil, err
		}
		return &swapAfterFinalSyncDirectory{anchoredDirectory: directory, parent: parent}, nil
	}
	result, err := writeNoReplace(context.Background(), target, []byte("payload"), ops)
	var writeErr *Error
	if !errors.As(err, &writeErr) || !errors.Is(err, ErrUnsafePath) || writeErr.Stage != StageSyncCleanup || !writeErr.Installed || !result.Installed {
		t.Fatalf("result=%+v error=%v, want installed StageSyncCleanup ErrUnsafePath", result, err)
	}
	if data, readErr := os.ReadFile(filepath.Join(parent+".real", "target")); readErr != nil || string(data) != "payload" {
		t.Fatalf("linearized target = %q, %v", data, readErr)
	}
}

type swapAfterFinalSyncDirectory struct {
	anchoredDirectory
	parent    string
	syncCalls int
}

func (directory *swapAfterFinalSyncDirectory) Sync() error {
	directory.syncCalls++
	if err := directory.anchoredDirectory.Sync(); err != nil {
		return err
	}
	if directory.syncCalls != 2 {
		return nil
	}
	moved := directory.parent + ".real"
	if err := os.Rename(directory.parent, moved); err != nil {
		return err
	}
	return os.Symlink(moved, directory.parent)
}

type faultOperations struct {
	failStage           Stage
	injected            error
	cleanupStage        Stage
	cleanupInjected     error
	cleanupSyncInjected error
	syncCalls           int
	closeCalls          int
}

func newFaultOperations(stage Stage, injected error) *faultOperations {
	return &faultOperations{failStage: stage, injected: injected}
}

func (fault *faultOperations) operations() operations {
	ops := defaultOperations()
	originalOpen := ops.openParent
	ops.openParent = func(path string, info fs.FileInfo) (anchoredDirectory, error) {
		directory, err := originalOpen(path, info)
		if err != nil {
			return nil, err
		}
		return &faultDirectory{anchoredDirectory: directory, fault: fault}, nil
	}
	return ops
}

type faultDirectory struct {
	anchoredDirectory
	fault *faultOperations
}

func (directory *faultDirectory) CreateTemp(prefix string) (durableFile, string, error) {
	if directory.fault.failStage == StageCreateTemp {
		return nil, "", directory.fault.injected
	}
	file, name, err := directory.anchoredDirectory.CreateTemp(prefix)
	if err != nil {
		return nil, "", err
	}
	return &faultFile{durableFile: file, fault: directory.fault}, name, nil
}

func (directory *faultDirectory) Install(oldName, newName string, expected fs.FileInfo) (bool, error) {
	if directory.fault.failStage == StageInstall {
		return false, directory.fault.injected
	}
	return directory.anchoredDirectory.Install(oldName, newName, expected)
}

func (directory *faultDirectory) Remove(name string) error {
	if directory.fault.failStage == StageRemoveTemp {
		return directory.fault.injected
	}
	if directory.fault.cleanupStage == StageRemoveTemp {
		return directory.fault.cleanupInjected
	}
	return directory.anchoredDirectory.Remove(name)
}

func (directory *faultDirectory) Sync() error {
	directory.fault.syncCalls++
	if directory.fault.failStage == StageSyncInstall && directory.fault.syncCalls == 1 {
		return directory.fault.injected
	}
	if directory.fault.failStage == StageSyncCleanup && directory.fault.syncCalls == 2 {
		return directory.fault.injected
	}
	if directory.fault.cleanupStage == StageSyncCleanup {
		return directory.fault.cleanupInjected
	}
	if directory.fault.cleanupSyncInjected != nil {
		return directory.fault.cleanupSyncInjected
	}
	return directory.anchoredDirectory.Sync()
}

type faultFile struct {
	durableFile
	fault *faultOperations
}

func (file *faultFile) Chmod(mode fs.FileMode) error {
	if file.fault.failStage == StageSetMode {
		return file.fault.injected
	}
	return file.durableFile.Chmod(mode)
}

func (file *faultFile) Write(value []byte) (int, error) {
	if file.fault.failStage == StageWrite {
		return 0, file.fault.injected
	}
	if file.fault.failStage == StageShortWrite {
		return len(value) - 1, nil
	}
	return file.durableFile.Write(value)
}

func (file *faultFile) Sync() error {
	if file.fault.failStage == StageSyncFile {
		return file.fault.injected
	}
	return file.durableFile.Sync()
}

func (file *faultFile) Close() error {
	file.fault.closeCalls++
	if file.fault.failStage == StageCloseFile {
		_ = file.durableFile.Close()
		return file.fault.injected
	}
	if file.fault.cleanupStage == StageCloseFile {
		_ = file.durableFile.Close()
		return file.fault.cleanupInjected
	}
	return file.durableFile.Close()
}

func writeFixtureFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func safeTempDir(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	return directory
}

func assertNoTemps(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file leaked: %s", entry.Name())
		}
	}
}
