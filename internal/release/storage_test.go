package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/immutablefile"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
)

func TestWriteLoadAndSyncManifestRoundTripWithoutRewrite(t *testing.T) {
	ctx := context.Background()
	root := realTempDir(t)
	manifest := testManifest(t)
	if err := WriteManifest(ctx, root, manifest); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	path := testManifestPath(root, manifest.InputDigest)
	beforeData, beforeInfo := readManifestFile(t, path)

	loaded, err := LoadManifest(ctx, root, manifest.InputDigest)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if !reflect.DeepEqual(loaded, manifest) {
		t.Fatalf("loaded manifest = %#v, want %#v", loaded, manifest)
	}
	synchronized, err := LoadAndSyncManifest(ctx, root, manifest.InputDigest)
	if err != nil {
		t.Fatalf("LoadAndSyncManifest: %v", err)
	}
	if !reflect.DeepEqual(synchronized, manifest) {
		t.Fatalf("synchronized manifest = %#v, want %#v", synchronized, manifest)
	}
	if err := SyncManifest(ctx, root, manifest.InputDigest); err != nil {
		t.Fatalf("SyncManifest: %v", err)
	}
	afterData, afterInfo := readManifestFile(t, path)
	if !bytes.Equal(beforeData, afterData) || beforeInfo.Mode() != afterInfo.Mode() || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatalf("SyncManifest mutated bytes/mode/mtime: before=%v after=%v", beforeInfo, afterInfo)
	}

	err = WriteManifest(ctx, root, manifest)
	if !errors.Is(err, ErrSnapshotExists) || !errors.Is(err, immutablefile.ErrExists) {
		t.Fatalf("second WriteManifest error = %v, want immutable exists conflict", err)
	}
	finalData, finalInfo := readManifestFile(t, path)
	if !bytes.Equal(beforeData, finalData) || beforeInfo.Mode() != finalInfo.Mode() || !beforeInfo.ModTime().Equal(finalInfo.ModTime()) {
		t.Fatal("exists conflict changed the installed manifest")
	}
	assertNoManifestTemps(t, filepath.Dir(path))
}

func TestManifestStorageValidatesDigestBeforeDerivingPath(t *testing.T) {
	root := realTempDir(t)
	invalid := inputdigest.Digest("../../outside")
	manifest := testManifest(t)
	manifest.InputDigest = invalid
	if err := WriteManifest(context.Background(), root, manifest); !errors.Is(err, inputdigest.ErrInvalidDigest) {
		t.Fatalf("WriteManifest error = %v, want ErrInvalidDigest", err)
	}
	if _, err := LoadManifest(context.Background(), root, invalid); !errors.Is(err, inputdigest.ErrInvalidDigest) {
		t.Fatalf("LoadManifest error = %v, want ErrInvalidDigest", err)
	}
	if _, err := LoadAndSyncManifest(context.Background(), root, invalid); !errors.Is(err, inputdigest.ErrInvalidDigest) {
		t.Fatalf("LoadAndSyncManifest error = %v, want ErrInvalidDigest", err)
	}
	if err := SyncManifest(context.Background(), root, invalid); !errors.Is(err, inputdigest.ErrInvalidDigest) {
		t.Fatalf("SyncManifest error = %v, want ErrInvalidDigest", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid digest created entries: %v", entries)
	}
}

func TestWriteManifestRejectsOversizedCanonicalBytesBeforeCreatingParents(t *testing.T) {
	root := filepath.Join(realTempDir(t), "evidence")
	manifest := testManifest(t)
	manifest.Selections[0].LabID = strings.Repeat("a", MaxManifestBytes)
	if err := WriteManifest(context.Background(), root, manifest); !errors.Is(err, ErrManifestTooLarge) {
		t.Fatalf("WriteManifest error = %v, want ErrManifestTooLarge", err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("oversized manifest created evidence root: %v", err)
	}
}

func TestManifestStorageRejectsSymlinkComponentsAndTargets(t *testing.T) {
	manifest := testManifest(t)
	digestDirectory := filepath.Base(filepath.Dir(testManifestPath("/placeholder", manifest.InputDigest)))
	tests := []struct {
		name      string
		prepare   func(*testing.T, string)
		writeWant error
		loadWant  error
	}{
		{
			name: "evidence root",
			prepare: func(t *testing.T, root string) {
				target := realTempDir(t)
				if err := os.Symlink(target, root); err != nil {
					t.Fatalf("Symlink evidence root: %v", err)
				}
			},
			writeWant: ErrSnapshotUnsafePath, loadWant: ErrSnapshotUnsafePath,
		},
		{
			name: "releases directory",
			prepare: func(t *testing.T, root string) {
				if err := os.Mkdir(root, 0o755); err != nil {
					t.Fatalf("Mkdir root: %v", err)
				}
				if err := os.Symlink(realTempDir(t), filepath.Join(root, "releases")); err != nil {
					t.Fatalf("Symlink releases: %v", err)
				}
			},
			writeWant: ErrSnapshotUnsafePath, loadWant: ErrSnapshotUnsafePath,
		},
		{
			name: "digest directory",
			prepare: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "releases"), 0o755); err != nil {
					t.Fatalf("MkdirAll releases: %v", err)
				}
				if err := os.Symlink(realTempDir(t), filepath.Join(root, "releases", digestDirectory)); err != nil {
					t.Fatalf("Symlink digest: %v", err)
				}
			},
			writeWant: ErrSnapshotUnsafePath, loadWant: ErrSnapshotUnsafePath,
		},
		{
			name: "manifest target",
			prepare: func(t *testing.T, root string) {
				directory := filepath.Dir(testManifestPath(root, manifest.InputDigest))
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatalf("MkdirAll digest: %v", err)
				}
				if err := os.Symlink(filepath.Join(realTempDir(t), "target"), filepath.Join(directory, "manifest.yaml")); err != nil {
					t.Fatalf("Symlink manifest: %v", err)
				}
			},
			writeWant: ErrSnapshotExists, loadWant: ErrSnapshotUnsafePath,
		},
		{
			name: "nonregular manifest target",
			prepare: func(t *testing.T, root string) {
				path := testManifestPath(root, manifest.InputDigest)
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatalf("MkdirAll manifest target: %v", err)
				}
			},
			writeWant: ErrSnapshotExists, loadWant: ErrSnapshotUnsafePath,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := realTempDir(t)
			root := filepath.Join(base, "evidence")
			test.prepare(t, root)
			if err := WriteManifest(context.Background(), root, manifest); !errors.Is(err, test.writeWant) {
				t.Fatalf("WriteManifest error = %v, want %v", err, test.writeWant)
			}
			if _, err := LoadManifest(context.Background(), root, manifest.InputDigest); !errors.Is(err, test.loadWant) {
				t.Fatalf("LoadManifest error = %v, want %v", err, test.loadWant)
			}
		})
	}
}

func TestManifestStorageRejectsCaseAliasedDirectoryAndTargetEntries(t *testing.T) {
	manifest := testManifest(t)
	encoded, err := Encode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	digestDirectory := filepath.Base(filepath.Dir(testManifestPath("/placeholder", manifest.InputDigest)))
	tests := []struct {
		name       string
		actualPath func(string) string
	}{
		{name: "releases directory", actualPath: func(root string) string {
			return filepath.Join(root, "RELEASES", digestDirectory, manifestFileName)
		}},
		{name: "digest directory", actualPath: func(root string) string {
			return filepath.Join(root, manifestDirectoryName, strings.ToUpper(digestDirectory[:6])+digestDirectory[6:], manifestFileName)
		}},
		{name: "manifest target", actualPath: func(root string) string {
			return filepath.Join(root, manifestDirectoryName, digestDirectory, "MANIFEST.YAML")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := realTempDir(t)
			actualPath := test.actualPath(root)
			writeRawManifest(t, actualPath, encoded)
			canonicalPath := testManifestPath(root, manifest.InputDigest)
			if _, err := os.Lstat(canonicalPath); errors.Is(err, fs.ErrNotExist) {
				t.Skip("filesystem treats case variants as distinct entries")
			} else if err != nil {
				t.Fatalf("Lstat canonical alias: %v", err)
			}

			if err := WriteManifest(context.Background(), root, manifest); !errors.Is(err, ErrSnapshotUnsafePath) {
				t.Fatalf("WriteManifest error = %v, want ErrSnapshotUnsafePath", err)
			}
			if _, err := LoadManifest(context.Background(), root, manifest.InputDigest); !errors.Is(err, ErrSnapshotUnsafePath) {
				t.Fatalf("LoadManifest error = %v, want ErrSnapshotUnsafePath", err)
			}
			if err := SyncManifest(context.Background(), root, manifest.InputDigest); !errors.Is(err, ErrSnapshotUnsafePath) {
				t.Fatalf("SyncManifest error = %v, want ErrSnapshotUnsafePath", err)
			}
		})
	}
}

func TestLoadManifestRejectsMissingOversizedCorruptAndPathMismatch(t *testing.T) {
	ctx := context.Background()
	manifest := testManifest(t)
	t.Run("missing", func(t *testing.T) {
		if _, err := LoadManifest(ctx, realTempDir(t), manifest.InputDigest); !errors.Is(err, ErrSnapshotNotFound) {
			t.Fatalf("LoadManifest error = %v, want ErrSnapshotNotFound", err)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		root := realTempDir(t)
		path := testManifestPath(root, manifest.InputDigest)
		writeRawManifest(t, path, bytes.Repeat([]byte("x"), MaxManifestBytes+1))
		if _, err := LoadManifest(ctx, root, manifest.InputDigest); !errors.Is(err, ErrManifestTooLarge) {
			t.Fatalf("LoadManifest error = %v, want ErrManifestTooLarge", err)
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		root := realTempDir(t)
		path := testManifestPath(root, manifest.InputDigest)
		writeRawManifest(t, path, []byte("not: canonical\n"))
		if _, err := LoadManifest(ctx, root, manifest.InputDigest); !errors.Is(err, ErrSnapshotCorrupt) {
			t.Fatalf("LoadManifest error = %v, want ErrSnapshotCorrupt", err)
		}
	})
	t.Run("path mismatch", func(t *testing.T) {
		root := realTempDir(t)
		other := manifest
		other.InputDigest = testInputDigest("b")
		encoded, err := Encode(other)
		if err != nil {
			t.Fatalf("Encode other: %v", err)
		}
		writeRawManifest(t, testManifestPath(root, manifest.InputDigest), encoded)
		if _, err := LoadManifest(ctx, root, manifest.InputDigest); !errors.Is(err, ErrSnapshotCorrupt) {
			t.Fatalf("LoadManifest error = %v, want ErrSnapshotCorrupt", err)
		}
	})
}

func TestManifestSecondaryLstatOperationalErrorsRemainIO(t *testing.T) {
	ctx := context.Background()
	root := realTempDir(t)
	manifest := testManifest(t)
	if err := WriteManifest(ctx, root, manifest); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	digestDirectory := filepath.Base(filepath.Dir(testManifestPath(root, manifest.InputDigest)))
	tests := []struct {
		name        string
		lstatName   string
		failureCall int
		cause       error
	}{
		{
			name:        "target after open",
			lstatName:   manifestFileName,
			failureCall: 2,
			cause:       &fs.PathError{Op: "lstat", Path: "injected-target-open", Err: syscall.EIO},
		},
		{
			name:        "target after read",
			lstatName:   manifestFileName,
			failureCall: 3,
			cause:       &fs.PathError{Op: "lstat", Path: "injected-target-read", Err: fs.ErrPermission},
		},
		{
			name:        "chain verification",
			lstatName:   digestDirectory,
			failureCall: 3,
			cause:       errors.New("injected chain verification I/O"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations := defaultManifestStorageOperations()
			failLstatCall(&operations, test.lstatName, test.failureCall, test.cause)
			got, err := loadManifestWithOperations(ctx, root, manifest.InputDigest, false, operations)
			if !reflect.DeepEqual(got, Manifest{}) || !errors.Is(err, ErrSnapshotIO) || !errors.Is(err, test.cause) || errors.Is(err, ErrSnapshotUnsafePath) {
				t.Fatalf("load result = (%#v, %v), want zero and operational I/O cause only", got, err)
			}
		})
	}
}

func TestManifestSecondaryLstatNotExistRemainsSemanticUnsafe(t *testing.T) {
	ctx := context.Background()
	root := realTempDir(t)
	manifest := testManifest(t)
	if err := WriteManifest(ctx, root, manifest); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	operations := defaultManifestStorageOperations()
	failLstatCall(&operations, manifestFileName, 2, fs.ErrNotExist)
	_, err := loadManifestWithOperations(ctx, root, manifest.InputDigest, false, operations)
	if !errors.Is(err, ErrSnapshotUnsafePath) || !errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrSnapshotIO) {
		t.Fatalf("load error = %v, want semantic unsafe with preserved not-exist cause", err)
	}
}

func TestManifestOpenReconciliationClassifiesOperationalLstatAsIO(t *testing.T) {
	base := realTempDir(t)
	if err := os.Mkdir(filepath.Join(base, "child"), 0o755); err != nil {
		t.Fatalf("Mkdir child: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, manifestFileName), []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer root.Close()
	rootInfo, err := inspectManifestDirectory(root, base)
	if err != nil {
		t.Fatalf("inspectManifestDirectory: %v", err)
	}
	parent := manifestDirectoryEntry{root: root, path: base, info: rootInfo}
	openCause := errors.New("injected open failure")

	tests := []struct {
		name      string
		entryName string
		reconcile func(fs.FileInfo, manifestStorageOperations) error
	}{
		{
			name:      "directory anchor",
			entryName: "child",
			reconcile: func(expected fs.FileInfo, operations manifestStorageOperations) error {
				return reconcileDirectoryOpenError(parent, "child", expected, openCause, operations)
			},
		},
		{
			name:      "manifest target",
			entryName: manifestFileName,
			reconcile: func(expected fs.FileInfo, operations manifestStorageOperations) error {
				return reconcileManifestOpenError(parent, expected, openCause, operations)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected, lstatErr := root.Lstat(test.entryName)
			if lstatErr != nil {
				t.Fatalf("Lstat fixture: %v", lstatErr)
			}
			cause := &fs.PathError{Op: "lstat", Path: "injected-reconcile", Err: syscall.EIO}
			operations := defaultManifestStorageOperations()
			operations.lstat = func(*os.Root, string) (fs.FileInfo, error) { return nil, cause }
			reconcileErr := test.reconcile(expected, operations)
			if !errors.Is(reconcileErr, ErrSnapshotIO) || !errors.Is(reconcileErr, cause) || !errors.Is(reconcileErr, openCause) || errors.Is(reconcileErr, ErrSnapshotUnsafePath) {
				t.Fatalf("reconcile error = %v, want operational I/O with both causes", reconcileErr)
			}
		})
	}
}

func TestLoadManifestRejectsTransientShortReadWithRestoredFileSnapshot(t *testing.T) {
	ctx := context.Background()
	root := realTempDir(t)
	first := testExpectedCell()
	second := testSecondExpectedCell()
	firstRecord := testRecord(t, first, testEvidenceID(1), testRunSetID(1), "b")
	secondRecord := testRecord(t, second, testEvidenceID(2), testRunSetID(1), "b")
	longManifest, err := Build(testInputDigest("a"), []ExpectedCell{first, second}, []evidence.Record{firstRecord, secondRecord})
	if err != nil {
		t.Fatalf("Build long manifest: %v", err)
	}
	shortManifest, err := Build(testInputDigest("a"), []ExpectedCell{first}, []evidence.Record{firstRecord})
	if err != nil {
		t.Fatalf("Build short manifest: %v", err)
	}
	shortBytes, err := Encode(shortManifest)
	if err != nil {
		t.Fatalf("Encode short manifest: %v", err)
	}
	longBytes, err := Encode(longManifest)
	if err != nil {
		t.Fatalf("Encode long manifest: %v", err)
	}
	if len(shortBytes) >= len(longBytes) {
		t.Fatalf("short fixture length=%d, long=%d", len(shortBytes), len(longBytes))
	}
	if err := WriteManifest(ctx, root, longManifest); err != nil {
		t.Fatalf("WriteManifest long: %v", err)
	}

	operations := defaultManifestStorageOperations()
	operations.readManifest = func(context.Context, io.Reader) ([]byte, error) {
		// Model observing the shorter valid B while the same inode's final stat
		// has already returned to the longer installed A snapshot.
		return append([]byte(nil), shortBytes...), nil
	}
	got, err := loadManifestWithOperations(ctx, root, longManifest.InputDigest, false, operations)
	if !reflect.DeepEqual(got, Manifest{}) || !errors.Is(err, ErrSnapshotUnsafePath) {
		t.Fatalf("load result = (%#v, %v), want rejected transient short snapshot", got, err)
	}
}

func TestReadManifestBytesEnforcesExactAllocationCap(t *testing.T) {
	tests := []struct {
		name       string
		available  int
		wantLength int
		wantLarge  bool
	}{
		{name: "exact max", available: MaxManifestBytes, wantLength: MaxManifestBytes},
		{name: "exact max plus one", available: MaxManifestBytes + 1, wantLarge: true},
		{name: "growing beyond max plus one", available: MaxManifestBytes + 2*(32*1024), wantLarge: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &recordingManifestReader{remaining: test.available}
			data, err := readManifestBytes(context.Background(), reader)
			if test.wantLarge {
				if !errors.Is(err, ErrManifestTooLarge) {
					t.Fatalf("read error = %v, want ErrManifestTooLarge", err)
				}
				if reader.total != MaxManifestBytes+1 || reader.requests[len(reader.requests)-1] != 1 {
					t.Fatalf("read retained beyond cap: total=%d final request=%d", reader.total, reader.requests[len(reader.requests)-1])
				}
				return
			}
			if err != nil || len(data) != test.wantLength || cap(data) > MaxManifestBytes+1 {
				t.Fatalf("read result len=%d cap=%d err=%v", len(data), cap(data), err)
			}
		})
	}
}

func TestWriteManifestPreservesEveryImmutableWriterStage(t *testing.T) {
	stages := []struct {
		stage     immutablefile.Stage
		installed bool
	}{
		{stage: immutablefile.StageValidatePath},
		{stage: immutablefile.StageCreateTemp},
		{stage: immutablefile.StageSetMode},
		{stage: immutablefile.StageWrite},
		{stage: immutablefile.StageShortWrite},
		{stage: immutablefile.StageSyncFile},
		{stage: immutablefile.StageCloseFile},
		{stage: immutablefile.StageInstall},
		{stage: immutablefile.StageSyncInstall, installed: true},
		{stage: immutablefile.StageRemoveTemp, installed: true},
		{stage: immutablefile.StageSyncCleanup, installed: true},
		{stage: immutablefile.StageCloseDirectory, installed: true},
	}
	for _, test := range stages {
		t.Run(string(test.stage), func(t *testing.T) {
			root := realTempDir(t)
			cause := errors.New("injected " + string(test.stage))
			operations := defaultManifestStorageOperations()
			writeCalls := 0
			operations.writeNoReplace = func(context.Context, string, fs.FileInfo, []byte) (immutablefile.Result, error) {
				writeCalls++
				return immutablefile.Result{Installed: test.installed}, &immutablefile.Error{Stage: test.stage, Installed: test.installed, Err: cause}
			}
			err := writeManifestWithOperations(context.Background(), root, testManifest(t), operations)
			var writeErr *immutablefile.Error
			if writeCalls != 1 || !errors.As(err, &writeErr) || writeErr.Stage != test.stage || writeErr.Installed != test.installed || !errors.Is(err, cause) {
				t.Fatalf("error = %v, want stage=%s installed=%t cause", err, test.stage, test.installed)
			}
			// The injected Task 8 writer owns the complete temporary-file
			// lifecycle; release itself must not manufacture a second temp.
			assertNoManifestTemps(t, filepath.Dir(testManifestPath(root, testInputDigest("a"))))
		})
	}
}

func TestWriteManifestExistsWithOperationalCompletionIsNotPureConflict(t *testing.T) {
	tests := []struct {
		name  string
		stage immutablefile.Stage
	}{
		{name: "install extra cause", stage: immutablefile.StageInstall},
		{name: "close file cleanup", stage: immutablefile.StageCloseFile},
		{name: "remove temp cleanup", stage: immutablefile.StageRemoveTemp},
		{name: "sync cleanup", stage: immutablefile.StageSyncCleanup},
		{name: "close directory cleanup", stage: immutablefile.StageCloseDirectory},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opaque := errors.New("injected operational cleanup failure")
			writerFailure := &immutablefile.Error{
				Stage:     test.stage,
				Installed: false,
				Err:       errors.Join(immutablefile.ErrExists, opaque),
			}
			operations := defaultManifestStorageOperations()
			operations.writeNoReplace = func(context.Context, string, fs.FileInfo, []byte) (immutablefile.Result, error) {
				return immutablefile.Result{Installed: false}, writerFailure
			}

			err := writeManifestWithOperations(context.Background(), realTempDir(t), testManifest(t), operations)
			var gotWriter *immutablefile.Error
			if !errors.Is(err, ErrSnapshotIO) ||
				!errors.Is(err, ErrSnapshotExists) ||
				errors.Is(err, ErrSnapshotUnsafePath) ||
				!errors.Is(err, opaque) ||
				!errors.Is(err, immutablefile.ErrExists) ||
				!errors.As(err, &gotWriter) ||
				gotWriter.Stage != test.stage ||
				gotWriter.Installed {
				t.Fatalf("write error = %v, want exists+IO preserving stage=%s installed=false and opaque cause", err, test.stage)
			}
		})
	}
}

func TestWriteManifestPureExistsAndUnsafeExistsRemainDistinct(t *testing.T) {
	tests := []struct {
		name       string
		stage      immutablefile.Stage
		cause      error
		wantUnsafe bool
	}{
		{name: "preflight exists", stage: immutablefile.StageValidatePath, cause: immutablefile.ErrExists},
		{name: "install race exists", stage: immutablefile.StageInstall, cause: errors.Join(immutablefile.ErrExists, fs.ErrExist)},
		{name: "unsafe cleanup wins", stage: immutablefile.StageSyncCleanup, cause: errors.Join(immutablefile.ErrExists, immutablefile.ErrUnsafePath), wantUnsafe: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writerFailure := &immutablefile.Error{Stage: test.stage, Installed: false, Err: test.cause}
			operations := defaultManifestStorageOperations()
			operations.writeNoReplace = func(context.Context, string, fs.FileInfo, []byte) (immutablefile.Result, error) {
				return immutablefile.Result{Installed: false}, writerFailure
			}

			err := writeManifestWithOperations(context.Background(), realTempDir(t), testManifest(t), operations)
			var gotWriter *immutablefile.Error
			if !errors.As(err, &gotWriter) || gotWriter.Stage != test.stage || gotWriter.Installed || !errors.Is(err, immutablefile.ErrExists) {
				t.Fatalf("write error = %v, want preserved immutable failure", err)
			}
			if test.wantUnsafe {
				if !errors.Is(err, ErrSnapshotUnsafePath) || errors.Is(err, ErrSnapshotExists) || errors.Is(err, ErrSnapshotIO) {
					t.Fatalf("unsafe write error = %v, want unsafe only", err)
				}
				return
			}
			if !errors.Is(err, ErrSnapshotExists) || errors.Is(err, ErrSnapshotUnsafePath) || errors.Is(err, ErrSnapshotIO) {
				t.Fatalf("pure exists error = %v, want exists only", err)
			}
		})
	}
}

func TestWriteManifestUnsafeWithOperationalSiblingKeepsUnsafeAndIO(t *testing.T) {
	tests := []struct {
		name       string
		cause      func(error) error
		wantExists bool
		wantIO     bool
	}{
		{name: "pure unsafe", cause: func(error) error { return immutablefile.ErrUnsafePath }},
		{name: "pure invalid", cause: func(error) error { return immutablefile.ErrInvalid }},
		{name: "unsafe plus operational", cause: func(opaque error) error {
			return errors.Join(immutablefile.ErrUnsafePath, opaque)
		}, wantIO: true},
		{name: "exists unsafe plus operational", cause: func(opaque error) error {
			return errors.Join(immutablefile.ErrExists, immutablefile.ErrUnsafePath, opaque)
		}, wantIO: true},
		{name: "invalid plus operational", cause: func(opaque error) error {
			return errors.Join(immutablefile.ErrInvalid, opaque)
		}, wantIO: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opaque := errors.New("injected operational sibling")
			writerFailure := &immutablefile.Error{Stage: immutablefile.StageSyncCleanup, Installed: false, Err: test.cause(opaque)}
			operations := defaultManifestStorageOperations()
			operations.writeNoReplace = func(context.Context, string, fs.FileInfo, []byte) (immutablefile.Result, error) {
				return immutablefile.Result{Installed: false}, writerFailure
			}

			err := writeManifestWithOperations(context.Background(), realTempDir(t), testManifest(t), operations)
			var gotWriter *immutablefile.Error
			if !errors.Is(err, ErrSnapshotUnsafePath) || errors.Is(err, ErrSnapshotExists) != test.wantExists || errors.Is(err, ErrSnapshotIO) != test.wantIO || !errors.As(err, &gotWriter) || gotWriter != writerFailure {
				t.Fatalf("write error = %v, want unsafe=%t exists=%t IO=%t with original writer", err, true, test.wantExists, test.wantIO)
			}
			if test.wantIO && !errors.Is(err, opaque) {
				t.Fatalf("write error = %v, want opaque operational sibling", err)
			}
		})
	}
}

func TestWriteManifestPureExistsProofCoversWholeTreeAndWriterResult(t *testing.T) {
	tests := []struct {
		name        string
		stage       immutablefile.Stage
		result      immutablefile.Result
		writerCause error
		wrap        func(error, error) error
	}{
		{
			name: "top-level operational sibling", stage: immutablefile.StageInstall,
			writerCause: immutablefile.ErrExists,
			wrap:        func(writer, opaque error) error { return errors.Join(fmt.Errorf("wrapped writer: %w", writer), opaque) },
		},
		{
			name: "result installed mismatch", stage: immutablefile.StageInstall,
			result: immutablefile.Result{Installed: true}, writerCause: immutablefile.ErrExists,
		},
		{
			name: "unknown stage", stage: immutablefile.Stage("unknown-stage"),
			writerCause: immutablefile.ErrExists,
		},
		{
			name: "custom Is-only exists leaf", stage: immutablefile.StageInstall,
			writerCause: customExistsLeaf{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opaque := errors.New("injected top-level operational sibling")
			writerFailure := &immutablefile.Error{Stage: test.stage, Installed: false, Err: test.writerCause}
			returned := error(writerFailure)
			if test.wrap != nil {
				returned = test.wrap(writerFailure, opaque)
			}
			operations := defaultManifestStorageOperations()
			operations.writeNoReplace = func(context.Context, string, fs.FileInfo, []byte) (immutablefile.Result, error) {
				return test.result, returned
			}

			err := writeManifestWithOperations(context.Background(), realTempDir(t), testManifest(t), operations)
			var gotWriter *immutablefile.Error
			if !errors.Is(err, ErrSnapshotExists) || !errors.Is(err, ErrSnapshotIO) || errors.Is(err, ErrSnapshotUnsafePath) || !errors.As(err, &gotWriter) || gotWriter != writerFailure {
				t.Fatalf("write error = %v, want non-pure exists+IO preserving original writer", err)
			}
			if test.wrap != nil && !errors.Is(err, opaque) {
				t.Fatalf("write error = %v, want top-level opaque sibling", err)
			}
		})
	}
}

func TestWriteManifestTypedNilImmutableWriterErrorIsOperational(t *testing.T) {
	var writerFailure *immutablefile.Error
	operations := defaultManifestStorageOperations()
	operations.writeNoReplace = func(context.Context, string, fs.FileInfo, []byte) (immutablefile.Result, error) {
		return immutablefile.Result{Installed: false}, writerFailure
	}

	err := writeManifestWithOperations(context.Background(), realTempDir(t), testManifest(t), operations)
	if !errors.Is(err, ErrSnapshotIO) || errors.Is(err, ErrSnapshotExists) || errors.Is(err, ErrSnapshotUnsafePath) {
		t.Fatalf("write error = %v, want operational ErrSnapshotIO only", err)
	}
}

func TestClassifyManifestWriterErrorFailsClosedOnSelfReferentialUnaryError(t *testing.T) {
	const helperEnvironment = "WOMW_SELF_REFERENTIAL_WRITER_ERROR_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		cycle := &selfReferentialUnaryError{}
		cycle.cause = cycle
		writerFailure := &immutablefile.Error{
			Stage:     immutablefile.StageInstall,
			Installed: false,
			Err:       errors.Join(immutablefile.ErrExists, cycle),
		}
		classified := classifyManifestWriterError(immutablefile.Result{Installed: false}, writerFailure)
		if !errors.Is(classified, ErrSnapshotIO) || !errors.Is(classified, ErrSnapshotExists) || !errors.Is(classified, immutablefile.ErrExists) || IsPureSnapshotExists(classified) {
			fmt.Fprintf(os.Stderr, "classified error = %v, want exists+IO and never pure exists\n", classified)
			os.Exit(2)
		}
		if errors.Is(classified, ErrSnapshotUnsafePath) || errors.Is(classified, errors.New("unrelated target")) {
			fmt.Fprintf(os.Stderr, "classified error = %v, want bounded negative errors.Is checks\n", classified)
			os.Exit(2)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestClassifyManifestWriterErrorFailsClosedOnSelfReferentialUnaryError$")
	command.Env = append(os.Environ(), helperEnvironment+"=1")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("classification did not terminate within the error-tree budget: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("classification helper failed: %v\n%s", err, output)
	}
}

func TestClassifyManifestWriterErrorFailsClosedWhenDepthBudgetIsExceeded(t *testing.T) {
	cause := error(immutablefile.ErrExists)
	for depth := 0; depth < 1024; depth++ {
		cause = fmt.Errorf("wrapper %d: %w", depth, cause)
	}
	writerFailure := &immutablefile.Error{
		Stage:     immutablefile.StageInstall,
		Installed: false,
		Err:       cause,
	}

	classified := classifyManifestWriterError(immutablefile.Result{Installed: false}, writerFailure)
	if !errors.Is(classified, ErrSnapshotIO) || IsPureSnapshotExists(classified) {
		t.Fatalf("classified error = %v, want bounded IO and never pure exists", classified)
	}
}

func TestClassifyManifestWriterErrorFailsClosedWhenNodeBudgetIsExceeded(t *testing.T) {
	causes := make([]error, 1024)
	for index := range causes {
		causes[index] = immutablefile.ErrExists
	}
	causes[0] = context.DeadlineExceeded
	writerFailure := &immutablefile.Error{
		Stage:     immutablefile.StageInstall,
		Installed: false,
		Err:       errors.Join(causes...),
	}

	classified := classifyManifestWriterError(immutablefile.Result{Installed: false}, writerFailure)
	if !errors.Is(classified, ErrSnapshotIO) || !errors.Is(classified, ErrSnapshotExists) || !errors.Is(classified, immutablefile.ErrExists) || !errors.Is(classified, context.DeadlineExceeded) || IsPureSnapshotExists(classified) {
		t.Fatalf("classified error = %v, want bounded context+exists+IO and never pure exists", classified)
	}
}

func TestPureSnapshotExistsClassificationIsStrict(t *testing.T) {
	writerFailure := &immutablefile.Error{
		Stage:     immutablefile.StageInstall,
		Installed: false,
		Err:       immutablefile.ErrExists,
	}
	classified := classifyManifestWriterError(immutablefile.Result{Installed: false}, writerFailure)
	if !IsPureSnapshotExists(classified) {
		t.Fatalf("classified error = %v, want certified pure snapshot exists", classified)
	}
	if !IsPureSnapshotExists(fmt.Errorf("wrapped classification: %w", classified)) {
		t.Fatal("single wrapper around certified conflict was not recognized")
	}

	opaque := errors.New("injected operational sibling")
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "public sentinel", err: ErrSnapshotExists},
		{name: "sentinel with opaque sibling", err: errors.Join(ErrSnapshotExists, opaque)},
		{name: "custom Is-only leaf", err: customSnapshotExistsLeaf{}},
		{name: "certified with opaque sibling", err: errors.Join(classified, opaque)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if IsPureSnapshotExists(test.err) {
				t.Fatalf("error = %v, want unproven snapshot exists", test.err)
			}
		})
	}
}

func TestWriteManifestPreservesOperationalWriterClassificationWhenVerificationIsSemantic(t *testing.T) {
	root := realTempDir(t)
	writerCause := errors.New("injected commit-uncertain writer failure")
	verificationCause := fs.ErrNotExist
	operations := defaultManifestStorageOperations()
	writeFinished := false
	operations.writeNoReplace = func(context.Context, string, fs.FileInfo, []byte) (immutablefile.Result, error) {
		writeFinished = true
		return immutablefile.Result{Installed: true}, &immutablefile.Error{
			Stage:     immutablefile.StageSyncInstall,
			Installed: true,
			Err:       writerCause,
		}
	}
	realLstat := operations.lstat
	operations.lstat = func(root *os.Root, name string) (fs.FileInfo, error) {
		if writeFinished {
			return nil, verificationCause
		}
		return realLstat(root, name)
	}

	err := writeManifestWithOperations(context.Background(), root, testManifest(t), operations)
	var writerErr *immutablefile.Error
	if !errors.Is(err, ErrSnapshotIO) ||
		!errors.Is(err, ErrSnapshotUnsafePath) ||
		!errors.Is(err, writerCause) ||
		!errors.Is(err, verificationCause) ||
		!errors.As(err, &writerErr) ||
		writerErr.Stage != immutablefile.StageSyncInstall ||
		!writerErr.Installed {
		t.Fatalf("write error = %v, want operational writer classification, preserved commit state, and semantic verification cause", err)
	}
}

func TestWriteManifestPostInstallFailureCanBeDurablyRetried(t *testing.T) {
	ctx := context.Background()
	root := realTempDir(t)
	manifest := testManifest(t)
	cause := errors.New("injected post-install failure")
	operations := defaultManifestStorageOperations()
	realWrite := operations.writeNoReplace
	operations.writeNoReplace = func(ctx context.Context, path string, expected fs.FileInfo, data []byte) (immutablefile.Result, error) {
		result, err := realWrite(ctx, path, expected, data)
		if err != nil {
			return result, err
		}
		return result, &immutablefile.Error{Stage: immutablefile.StageSyncInstall, Installed: true, Err: cause}
	}
	err := writeManifestWithOperations(ctx, root, manifest, operations)
	var writeErr *immutablefile.Error
	if !errors.As(err, &writeErr) || !writeErr.Installed || !errors.Is(err, cause) {
		t.Fatalf("write error = %v, want installed post-install failure", err)
	}
	if err := SyncManifest(ctx, root, manifest.InputDigest); err != nil {
		t.Fatalf("SyncManifest retry: %v", err)
	}
	loaded, err := LoadManifest(ctx, root, manifest.InputDigest)
	if err != nil || !reflect.DeepEqual(loaded, manifest) {
		t.Fatalf("LoadManifest retry = (%#v, %v)", loaded, err)
	}
	assertNoManifestTemps(t, filepath.Dir(testManifestPath(root, manifest.InputDigest)))
}

func TestConcurrentManifestWritersChooseOneImmutableWinner(t *testing.T) {
	root := realTempDir(t)
	first := testManifest(t)
	second := first
	second.Selections = append([]Selection{}, first.Selections...)
	second.Selections[0].ContentDigest = string(testInputDigest("b"))

	start := make(chan struct{})
	errorsByWriter := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for index, manifest := range []Manifest{first, second} {
		index := index
		manifest := manifest
		go func() {
			defer wait.Done()
			<-start
			errorsByWriter[index] = WriteManifest(context.Background(), root, manifest)
		}()
	}
	close(start)
	wait.Wait()

	successes := 0
	exists := 0
	for _, err := range errorsByWriter {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrSnapshotExists) {
			exists++
		} else {
			t.Fatalf("unexpected writer error: %v", err)
		}
	}
	if successes != 1 || exists != 1 {
		t.Fatalf("writer outcomes success=%d exists=%d errors=%v", successes, exists, errorsByWriter)
	}
	loaded, err := LoadManifest(context.Background(), root, first.InputDigest)
	if err != nil {
		t.Fatalf("LoadManifest winner: %v", err)
	}
	if !reflect.DeepEqual(loaded, first) && !reflect.DeepEqual(loaded, second) {
		t.Fatalf("winner = %#v, want one complete input", loaded)
	}
	assertNoManifestTemps(t, filepath.Dir(testManifestPath(root, first.InputDigest)))
}

func TestWriteManifestCorruptExistingTargetIsNeverReplaced(t *testing.T) {
	root := realTempDir(t)
	manifest := testManifest(t)
	path := testManifestPath(root, manifest.InputDigest)
	corrupt := []byte("corrupt\n")
	writeRawManifest(t, path, corrupt)
	before, info := readManifestFile(t, path)
	err := WriteManifest(context.Background(), root, manifest)
	if !errors.Is(err, ErrSnapshotExists) {
		t.Fatalf("WriteManifest error = %v, want ErrSnapshotExists", err)
	}
	after, afterInfo := readManifestFile(t, path)
	if !bytes.Equal(before, after) || info.Mode() != afterInfo.Mode() || !info.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("WriteManifest replaced or mutated corrupt existing target")
	}
	assertNoManifestTemps(t, filepath.Dir(path))
}

func TestManifestParentCreationSyncsEveryNewRelationship(t *testing.T) {
	root := filepath.Join(realTempDir(t), "one", "two", "evidence")
	operations := defaultManifestStorageOperations()
	realSync := operations.syncDirectory
	syncCalls := make([]string, 0)
	operations.syncDirectory = func(directory *os.Root, path string) error {
		syncCalls = append(syncCalls, path)
		return realSync(directory, path)
	}
	if err := writeManifestWithOperations(context.Background(), root, testManifest(t), operations); err != nil {
		t.Fatalf("writeManifestWithOperations: %v", err)
	}
	sorted := append([]string{}, syncCalls...)
	sort.Strings(sorted)
	for _, required := range []string{filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(root))), "one"), filepath.Join(filepath.Dir(filepath.Dir(root)), "two"), root, filepath.Join(root, "releases"), filepath.Dir(testManifestPath(root, testInputDigest("a")))} {
		if !containsString(sorted, required) {
			t.Fatalf("sync calls %v omit newly created relationship %q", syncCalls, required)
		}
	}
}

func TestManifestParentCreationFailureIsResynchronizedOnRetry(t *testing.T) {
	base := realTempDir(t)
	root := filepath.Join(base, "one", "two", "evidence")
	cause := errors.New("injected parent synchronization failure")
	first := defaultManifestStorageOperations()
	realFirstSync := first.syncDirectory
	failed := false
	first.syncDirectory = func(directory *os.Root, path string) error {
		if path == base && !failed {
			failed = true
			return cause
		}
		return realFirstSync(directory, path)
	}
	if err := writeManifestWithOperations(context.Background(), root, testManifest(t), first); !errors.Is(err, ErrSnapshotIO) || !errors.Is(err, cause) {
		t.Fatalf("first write error = %v, want classified injected sync failure", err)
	}
	if _, err := os.Lstat(testManifestPath(root, testInputDigest("a"))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("first write installed a target: %v", err)
	}

	second := defaultManifestStorageOperations()
	realSecondSync := second.syncDirectory
	var synchronized []string
	second.syncDirectory = func(directory *os.Root, path string) error {
		synchronized = append(synchronized, path)
		return realSecondSync(directory, path)
	}
	if err := writeManifestWithOperations(context.Background(), root, testManifest(t), second); err != nil {
		t.Fatalf("retry write: %v", err)
	}
	if !containsString(synchronized, base) {
		t.Fatalf("retry sync calls %v omit the preexisting uncertain parent %q", synchronized, base)
	}
}

func TestSyncAndLoadManifestPreserveOperationalFailures(t *testing.T) {
	ctx := context.Background()
	root := realTempDir(t)
	manifest := testManifest(t)
	if err := WriteManifest(ctx, root, manifest); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	path := testManifestPath(root, manifest.InputDigest)
	wantData, wantInfo := readManifestFile(t, path)

	t.Run("file sync", func(t *testing.T) {
		cause := errors.New("injected file sync failure")
		operations := defaultManifestStorageOperations()
		operations.syncFile = func(*os.File) error { return cause }
		got, err := loadManifestWithOperations(ctx, root, manifest.InputDigest, true, operations)
		if !reflect.DeepEqual(got, Manifest{}) || !errors.Is(err, ErrSnapshotIO) || !errors.Is(err, cause) {
			t.Fatalf("sync result = (%#v, %v), want zero and classified cause", got, err)
		}
	})

	t.Run("directory sync", func(t *testing.T) {
		cause := errors.New("injected directory sync failure")
		operations := defaultManifestStorageOperations()
		operations.syncDirectory = func(*os.Root, string) error { return cause }
		got, err := loadManifestWithOperations(ctx, root, manifest.InputDigest, true, operations)
		if !reflect.DeepEqual(got, Manifest{}) || !errors.Is(err, ErrSnapshotIO) || !errors.Is(err, cause) {
			t.Fatalf("sync result = (%#v, %v), want zero and classified cause", got, err)
		}
	})

	t.Run("file close", func(t *testing.T) {
		cause := errors.New("injected file close failure")
		operations := defaultManifestStorageOperations()
		realClose := operations.closeFile
		operations.closeFile = func(file *os.File) error {
			return errors.Join(realClose(file), cause)
		}
		got, err := loadManifestWithOperations(ctx, root, manifest.InputDigest, false, operations)
		if !reflect.DeepEqual(got, Manifest{}) || !errors.Is(err, ErrSnapshotIO) || !errors.Is(err, cause) {
			t.Fatalf("load result = (%#v, %v), want zero and classified cause", got, err)
		}
	})

	gotData, gotInfo := readManifestFile(t, path)
	if !bytes.Equal(gotData, wantData) || gotInfo.Mode() != wantInfo.Mode() || !gotInfo.ModTime().Equal(wantInfo.ModTime()) {
		t.Fatal("failed load/sync operation mutated manifest bytes, mode, or mtime")
	}
}

func TestManifestStorageHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manifest := testManifest(t)
	root := realTempDir(t)
	if err := WriteManifest(ctx, root, manifest); !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteManifest error = %v, want context.Canceled", err)
	}
	if _, err := LoadManifest(ctx, root, manifest.InputDigest); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadManifest error = %v, want context.Canceled", err)
	}
	if _, err := LoadAndSyncManifest(ctx, root, manifest.InputDigest); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadAndSyncManifest error = %v, want context.Canceled", err)
	}
	if err := SyncManifest(ctx, root, manifest.InputDigest); !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncManifest error = %v, want context.Canceled", err)
	}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks temp dir: %v", err)
	}
	return root
}

func testManifestPath(root string, digest inputdigest.Digest) string {
	hex := strings.TrimPrefix(string(digest), "sha256:")
	return filepath.Join(root, "releases", "sha256-"+hex, "manifest.yaml")
}

func writeRawManifest(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll raw manifest: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile raw manifest: %v", err)
	}
}

func readManifestFile(t *testing.T, path string) ([]byte, fs.FileInfo) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile manifest: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat manifest: %v", err)
	}
	return data, info
}

func assertNoManifestTemps(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir manifest directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".manifest.yaml.tmp-") {
			t.Fatalf("leaked manifest temporary file %q", entry.Name())
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func failLstatCall(operations *manifestStorageOperations, name string, failureCall int, cause error) {
	realLstat := operations.lstat
	calls := 0
	operations.lstat = func(root *os.Root, entryName string) (fs.FileInfo, error) {
		if entryName == name {
			calls++
			if calls == failureCall {
				return nil, cause
			}
		}
		return realLstat(root, entryName)
	}
}

type recordingManifestReader struct {
	remaining int
	total     int
	requests  []int
}

type customExistsLeaf struct{}

func (customExistsLeaf) Error() string {
	return "custom exists leaf"
}

func (customExistsLeaf) Is(target error) bool {
	return target == fs.ErrExist
}

type customSnapshotExistsLeaf struct{}

func (customSnapshotExistsLeaf) Error() string {
	return "custom release snapshot exists"
}

func (customSnapshotExistsLeaf) Is(target error) bool {
	return target == ErrSnapshotExists
}

type selfReferentialUnaryError struct {
	cause error
}

func (*selfReferentialUnaryError) Error() string {
	return "self-referential unary error"
}

func (err *selfReferentialUnaryError) Unwrap() error {
	return err.cause
}

func (reader *recordingManifestReader) Read(buffer []byte) (int, error) {
	reader.requests = append(reader.requests, len(buffer))
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	count := len(buffer)
	if count > reader.remaining {
		count = reader.remaining
	}
	for index := 0; index < count; index++ {
		buffer[index] = 'x'
	}
	reader.remaining -= count
	reader.total += count
	if reader.remaining == 0 {
		return count, io.EOF
	}
	return count, nil
}
