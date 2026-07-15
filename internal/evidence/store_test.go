package evidence

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/immutablefile"
)

func TestStorePutGetIsAppendOnlyAndReturnsIndependentRecords(t *testing.T) {
	store := newTestStore(t)
	record := sealedRecordWithEntropy(t, 1)
	if err := store.Put(context.Background(), record); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Put(context.Background(), record); !errors.Is(err, ErrEvidenceExists) {
		t.Fatalf("second Put error = %v, want ErrEvidenceExists", err)
	}

	record.Parameters["capacity"] = 99
	record.Workload.Parameters["capacity"] = 99
	record.Measurements["requests.total"] = Measurement{Unit: "changed", Value: 99}
	record.Assertions[0].Message = "changed"
	record.Limitations[0] = "changed"
	first, err := store.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if first.Parameters["capacity"] != 4 || first.Workload.Parameters["capacity"] != 4 || first.Measurements["requests.total"].Unit != "requests" || first.Assertions[0].Message != "" || first.Limitations[0] != "local deterministic model" {
		t.Fatalf("stored record aliases caller: %+v", first)
	}

	first.Parameters["capacity"] = 77
	first.Workload.Parameters["capacity"] = 77
	first.Assertions[0].Message = "returned alias"
	second, err := store.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if second.Parameters["capacity"] != 4 || second.Workload.Parameters["capacity"] != 4 || second.Assertions[0].Message != "" {
		t.Fatalf("returned record aliases store state: %+v", second)
	}
}

func TestStoreListSortsByEveryCellFieldThenEvidenceID(t *testing.T) {
	store := newTestStore(t)
	type fixture struct {
		entropy int
		key     CellKey
		adapter bool
	}
	want := []fixture{
		{entropy: 1, key: CellKey{LabID: "a", RequiredRunID: "a", BindingID: "a", ClaimID: "a", ImplementationID: "a"}},
		{entropy: 2, key: CellKey{LabID: "a", RequiredRunID: "a", BindingID: "a", ClaimID: "a", ImplementationID: "a"}},
		{entropy: 3, key: CellKey{LabID: "a", RequiredRunID: "a", BindingID: "a", ClaimID: "a", ImplementationID: "a", AdapterID: "a"}, adapter: true},
		{entropy: 4, key: CellKey{LabID: "a", RequiredRunID: "a", BindingID: "a", ClaimID: "a", ImplementationID: "b"}},
		{entropy: 5, key: CellKey{LabID: "a", RequiredRunID: "a", BindingID: "a", ClaimID: "b", ImplementationID: "a"}},
		{entropy: 6, key: CellKey{LabID: "a", RequiredRunID: "a", BindingID: "b", ClaimID: "a", ImplementationID: "a"}},
		{entropy: 7, key: CellKey{LabID: "a", RequiredRunID: "b", BindingID: "a", ClaimID: "a", ImplementationID: "a"}},
		{entropy: 8, key: CellKey{LabID: "b", RequiredRunID: "a", BindingID: "a", ClaimID: "a", ImplementationID: "a"}},
	}
	wantIDs := make([]string, 0, len(want))
	for index := len(want) - 1; index >= 0; index-- {
		fixture := want[index]
		record := validRecord()
		record.ID = attemptID(fixture.entropy)
		record.LabID = fixture.key.LabID
		record.RequiredRunID = fixture.key.RequiredRunID
		record.BindingID = fixture.key.BindingID
		record.ClaimID = fixture.key.ClaimID
		record.ImplementationID = fixture.key.ImplementationID
		record.AdapterID = fixture.key.AdapterID
		if fixture.adapter {
			record.Role = RoleAdapter
		}
		sealed, err := Seal(record)
		if err != nil {
			t.Fatalf("Seal fixture %d: %v", fixture.entropy, err)
		}
		if err := store.Put(context.Background(), sealed); err != nil {
			t.Fatalf("Put fixture %d: %v", fixture.entropy, err)
		}
	}
	for _, fixture := range want {
		wantIDs = append(wantIDs, attemptID(fixture.entropy))
	}
	records, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	gotIDs := make([]string, 0, len(records))
	for _, record := range records {
		gotIDs = append(gotIDs, record.ID)
	}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("List IDs = %v, want %v", gotIDs, wantIDs)
	}
}

func TestStoreValidatesIDsAndSealedRecordsBeforeIO(t *testing.T) {
	store := newTestStore(t)
	root := store.root
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("remove store root: %v", err)
	}
	publicError := ValidateID("../escape")
	if publicError == nil {
		t.Fatal("ValidateID accepted a path-shaped ID")
	}
	if _, err := store.Get(context.Background(), "../escape"); !errors.Is(err, ErrEvidenceInvalid) || !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("Get invalid ID error = %v", err)
	}
	invalid := validRecord()
	invalid.ID = "../escape"
	if err := store.Put(context.Background(), invalid); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("Put invalid ID error = %v", err)
	}
	unsealed := validRecord()
	if err := store.Put(context.Background(), unsealed); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("Put unsealed record error = %v", err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("invalid input recreated or touched root: %v", err)
	}
}

func TestStoreGetClassifiesEveryOperationalReadFailureAsIO(t *testing.T) {
	tests := []struct {
		name   string
		inject func(*testing.T, *Store, error)
	}{
		{name: "initial lstat", inject: func(t *testing.T, store *Store, injected error) {
			original := store.readOps.lstat
			calls := 0
			store.readOps.lstat = func(root *os.Root, name string) (fs.FileInfo, error) {
				calls++
				if calls == 1 {
					return nil, injected
				}
				return original(root, name)
			}
		}},
		{name: "open", inject: func(t *testing.T, store *Store, injected error) {
			store.readOps.openFile = func(*os.Root, string) (*os.File, error) {
				return nil, injected
			}
		}},
		{name: "pre-read stat", inject: func(t *testing.T, store *Store, injected error) {
			store.readOps.statFile = func(*os.File) (fs.FileInfo, error) {
				return nil, injected
			}
		}},
		{name: "verification lstat", inject: func(t *testing.T, store *Store, injected error) {
			original := store.readOps.lstat
			calls := 0
			store.readOps.lstat = func(root *os.Root, name string) (fs.FileInfo, error) {
				calls++
				if calls == 2 {
					return nil, injected
				}
				return original(root, name)
			}
		}},
		{name: "read", inject: func(t *testing.T, store *Store, injected error) {
			store.readOps.readFile = func(*os.File, int64) ([]byte, error) {
				return nil, injected
			}
		}},
		{name: "post-read stat", inject: func(t *testing.T, store *Store, injected error) {
			original := store.readOps.statFile
			calls := 0
			store.readOps.statFile = func(file *os.File) (fs.FileInfo, error) {
				calls++
				if calls == 2 {
					return nil, injected
				}
				return original(file)
			}
		}},
		{name: "close", inject: func(t *testing.T, store *Store, injected error) {
			original := store.readOps.closeFile
			store.readOps.closeFile = func(file *os.File) error {
				if err := original(file); err != nil {
					t.Fatalf("real close before injection: %v", err)
				}
				return injected
			}
		}},
		{name: "post-read lstat", inject: func(t *testing.T, store *Store, injected error) {
			original := store.readOps.lstat
			calls := 0
			store.readOps.lstat = func(root *os.Root, name string) (fs.FileInfo, error) {
				calls++
				if calls == 3 {
					return nil, injected
				}
				return original(root, name)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			record := sealedRecordWithEntropy(t, 1)
			if err := store.Put(context.Background(), record); err != nil {
				t.Fatalf("Put: %v", err)
			}
			injected := fmt.Errorf("injected %s failure", test.name)
			test.inject(t, store, injected)
			_, err := store.Get(context.Background(), record.ID)
			if !errors.Is(err, ErrEvidenceIO) || errors.Is(err, ErrEvidenceCorrupt) || !errors.Is(err, injected) {
				t.Fatalf("Get error = %v, want only ErrEvidenceIO wrapping %v", err, injected)
			}
		})
	}
}

func TestStoreListClassifiesEveryOperationalDirectoryFailureAsIO(t *testing.T) {
	tests := []struct {
		name   string
		inject func(*testing.T, *Store, error)
	}{
		{name: "open directory", inject: func(t *testing.T, store *Store, injected error) {
			store.readOps.openDirectory = func(*os.Root) (*os.File, error) {
				return nil, injected
			}
		}},
		{name: "read directory", inject: func(t *testing.T, store *Store, injected error) {
			store.readOps.readDirectory = func(*os.File) ([]fs.DirEntry, error) {
				return nil, injected
			}
		}},
		{name: "close directory", inject: func(t *testing.T, store *Store, injected error) {
			original := store.readOps.closeDirectory
			store.readOps.closeDirectory = func(directory *os.File) error {
				if err := original(directory); err != nil {
					t.Fatalf("real directory close before injection: %v", err)
				}
				return injected
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			injected := fmt.Errorf("injected %s failure", test.name)
			test.inject(t, store, injected)
			_, err := store.List(context.Background())
			if !errors.Is(err, ErrEvidenceIO) || errors.Is(err, ErrEvidenceCorrupt) || !errors.Is(err, injected) {
				t.Fatalf("List error = %v, want only ErrEvidenceIO wrapping %v", err, injected)
			}
		})
	}
}

func TestStoreListClassifiesEveryOperationalReopenFailureAsIO(t *testing.T) {
	tests := []struct {
		name   string
		inject func(*testing.T, *Store, error)
	}{
		{name: "open filesystem root", inject: func(t *testing.T, store *Store, injected error) {
			store.anchorOps.openRoot = func(string) (*os.Root, error) {
				return nil, injected
			}
		}},
		{name: "lstat component", inject: func(t *testing.T, store *Store, injected error) {
			store.anchorOps.lstat = func(*os.Root, string) (fs.FileInfo, error) {
				return nil, injected
			}
		}},
		{name: "open child root", inject: func(t *testing.T, store *Store, injected error) {
			store.anchorOps.openChild = func(*os.Root, string) (*os.Root, error) {
				return nil, injected
			}
		}},
		{name: "open child inspection", inject: func(t *testing.T, store *Store, injected error) {
			store.anchorOps.openDirectory = func(*os.Root) (*os.File, error) {
				return nil, injected
			}
		}},
		{name: "stat child inspection", inject: func(t *testing.T, store *Store, injected error) {
			store.anchorOps.statFile = func(*os.File) (fs.FileInfo, error) {
				return nil, injected
			}
		}},
		{name: "close child inspection", inject: func(t *testing.T, store *Store, injected error) {
			original := store.anchorOps.closeFile
			store.anchorOps.closeFile = func(file *os.File) error {
				return errors.Join(original(file), injected)
			}
		}},
		{name: "close root anchor", inject: func(t *testing.T, store *Store, injected error) {
			original := store.anchorOps.closeRoot
			store.anchorOps.closeRoot = func(root *os.Root) error {
				return errors.Join(original(root), injected)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			injected := fmt.Errorf("injected %s failure", test.name)
			test.inject(t, store, injected)
			_, err := store.List(context.Background())
			if !errors.Is(err, ErrEvidenceIO) || errors.Is(err, ErrEvidenceUnsafePath) || errors.Is(err, ErrEvidenceCorrupt) || !errors.Is(err, injected) {
				t.Fatalf("List error = %v, want only ErrEvidenceIO wrapping %v", err, injected)
			}
		})
	}
}

func TestStoreListPreservesPrimaryReopenAndCleanupCloseFailures(t *testing.T) {
	store := newTestStore(t)
	primary := errors.New("injected open child failure")
	closeFailure := errors.New("injected cleanup close failure")
	store.anchorOps.openChild = func(*os.Root, string) (*os.Root, error) {
		return nil, primary
	}
	originalClose := store.anchorOps.closeRoot
	store.anchorOps.closeRoot = func(root *os.Root) error {
		return errors.Join(originalClose(root), closeFailure)
	}
	_, err := store.List(context.Background())
	if !errors.Is(err, ErrEvidenceIO) || errors.Is(err, ErrEvidenceUnsafePath) || errors.Is(err, ErrEvidenceCorrupt) || !errors.Is(err, primary) || !errors.Is(err, closeFailure) {
		t.Fatalf("List error lost reopen/cleanup cause or taxonomy: %v", err)
	}
}

func TestStoreListKeepsMissingFrozenPathAsUnsafe(t *testing.T) {
	store := newTestStore(t)
	store.anchorOps.lstat = func(*os.Root, string) (fs.FileInfo, error) {
		return nil, fs.ErrNotExist
	}
	_, err := store.List(context.Background())
	if !errors.Is(err, ErrEvidenceUnsafePath) || errors.Is(err, ErrEvidenceIO) || errors.Is(err, ErrEvidenceCorrupt) {
		t.Fatalf("List error = %v, want only ErrEvidenceUnsafePath", err)
	}
}

func TestStoreGetClassifiesSnapshotMutationAsCorruptNotIO(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *Store, fs.FileInfo) (fs.FileInfo, error)
	}{
		{name: "disappeared", mutate: func(*testing.T, *Store, fs.FileInfo) (fs.FileInfo, error) {
			return nil, fs.ErrNotExist
		}},
		{name: "identity", mutate: func(t *testing.T, store *Store, _ fs.FileInfo) (fs.FileInfo, error) {
			other := filepath.Join(store.runs, ".other-record")
			if err := os.WriteFile(other, []byte("other"), 0o644); err != nil {
				t.Fatalf("write identity replacement: %v", err)
			}
			return os.Lstat(other)
		}},
		{name: "size", mutate: func(_ *testing.T, _ *Store, info fs.FileInfo) (fs.FileInfo, error) {
			return alteredFileInfo{FileInfo: info, sizeDelta: 1}, nil
		}},
		{name: "mtime", mutate: func(_ *testing.T, _ *Store, info fs.FileInfo) (fs.FileInfo, error) {
			return alteredFileInfo{FileInfo: info, modTimeDelta: time.Nanosecond}, nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			record := sealedRecordWithEntropy(t, 1)
			if err := store.Put(context.Background(), record); err != nil {
				t.Fatalf("Put: %v", err)
			}
			original := store.readOps.lstat
			calls := 0
			store.readOps.lstat = func(root *os.Root, name string) (fs.FileInfo, error) {
				calls++
				info, err := original(root, name)
				if calls == 3 && err == nil {
					return test.mutate(t, store, info)
				}
				return info, err
			}
			_, err := store.Get(context.Background(), record.ID)
			if !errors.Is(err, ErrEvidenceCorrupt) || errors.Is(err, ErrEvidenceIO) || errors.Is(err, ErrEvidenceNotFound) {
				t.Fatalf("Get error = %v, want only ErrEvidenceCorrupt", err)
			}
		})
	}
}

func TestStoreGetPreservesOpenFailureCauseAcrossSemanticPathMutation(t *testing.T) {
	tests := []struct {
		name      string
		want      error
		forbidden error
		mutate    func(*testing.T, string)
	}{
		{
			name:      "disappearance",
			want:      ErrEvidenceCorrupt,
			forbidden: ErrEvidenceUnsafePath,
			mutate: func(t *testing.T, target string) {
				if err := os.Remove(target); err != nil {
					t.Fatalf("remove record during open: %v", err)
				}
			},
		},
		{
			name:      "symlink replacement",
			want:      ErrEvidenceUnsafePath,
			forbidden: ErrEvidenceCorrupt,
			mutate: func(t *testing.T, target string) {
				if err := os.Remove(target); err != nil {
					t.Fatalf("remove record before symlink: %v", err)
				}
				outside := filepath.Join(filepath.Dir(filepath.Dir(target)), "open-race-outside")
				if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
					t.Fatalf("write symlink target: %v", err)
				}
				if err := os.Symlink(outside, target); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
		{
			name:      "identity replacement",
			want:      ErrEvidenceCorrupt,
			forbidden: ErrEvidenceUnsafePath,
			mutate: func(t *testing.T, target string) {
				replacement := target + ".replacement"
				if err := os.WriteFile(replacement, []byte("replacement"), 0o644); err != nil {
					t.Fatalf("write identity replacement: %v", err)
				}
				if err := os.Rename(replacement, target); err != nil {
					t.Fatalf("install identity replacement: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			record := sealedRecordWithEntropy(t, 1)
			if err := store.Put(context.Background(), record); err != nil {
				t.Fatalf("Put: %v", err)
			}
			target := filepath.Join(store.runs, record.ID+".json")
			openFailure := errors.New("injected open failure")
			store.readOps.openFile = func(*os.Root, string) (*os.File, error) {
				test.mutate(t, target)
				return nil, openFailure
			}

			_, err := store.Get(context.Background(), record.ID)
			if !errors.Is(err, test.want) || errors.Is(err, test.forbidden) || errors.Is(err, ErrEvidenceIO) || !errors.Is(err, openFailure) {
				t.Fatalf("Get error = %v, want %v wrapping open failure, excluding %v/ErrEvidenceIO", err, test.want, test.forbidden)
			}
		})
	}
}

func TestNewStoreRejectsUnsafeDirectoryComponents(t *testing.T) {
	parent := safeEvidenceTempDir(t)
	realRoot := filepath.Join(parent, "real")
	if err := os.Mkdir(realRoot, 0o755); err != nil {
		t.Fatalf("mkdir real root: %v", err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := NewStore(alias); !errors.Is(err, ErrEvidenceUnsafePath) {
		t.Fatalf("symlink root error = %v", err)
	}

	nondirectory := filepath.Join(parent, "file")
	if err := os.WriteFile(nondirectory, []byte("x"), 0o644); err != nil {
		t.Fatalf("write nondirectory: %v", err)
	}
	if _, err := NewStore(nondirectory); !errors.Is(err, ErrEvidenceUnsafePath) {
		t.Fatalf("nondirectory root error = %v", err)
	}

	rootWithAlias := filepath.Join(parent, "evidence")
	if err := os.Mkdir(rootWithAlias, 0o755); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}
	if err := os.Symlink(realRoot, filepath.Join(rootWithAlias, "runs")); err != nil {
		t.Skipf("runs symlink unavailable: %v", err)
	}
	if _, err := NewStore(rootWithAlias); !errors.Is(err, ErrEvidenceUnsafePath) {
		t.Fatalf("symlink runs error = %v", err)
	}
}

func TestOpenStoreReadOnlyNeverCreatesMissingRootOrRuns(t *testing.T) {
	parent := safeEvidenceTempDir(t)
	tests := []struct {
		name        string
		prepareRoot bool
	}{
		{name: "missing root"},
		{name: "missing runs", prepareRoot: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(parent, strings.ReplaceAll(test.name, " ", "-"))
			if test.prepareRoot {
				if err := os.Mkdir(root, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := OpenStoreReadOnly(root); !errors.Is(err, ErrEvidenceNotFound) {
				t.Fatalf("OpenStoreReadOnly error = %v, want ErrEvidenceNotFound", err)
			}
			if _, err := os.Lstat(filepath.Join(root, "runs")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("read-only open created runs: %v", err)
			}
			if !test.prepareRoot {
				if _, err := os.Lstat(root); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("read-only open created root: %v", err)
				}
			}
		})
	}
}

func TestOpenStoreReadOnlyReadsExistingRecordsButRejectsPut(t *testing.T) {
	writable := newTestStore(t)
	record := sealedRecordWithEntropy(t, 1)
	if err := writable.Put(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	readOnly, err := OpenStoreReadOnly(writable.root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := readOnly.Get(context.Background(), record.ID)
	if err != nil || loaded.ID != record.ID {
		t.Fatalf("read-only Get = (%#v, %v)", loaded, err)
	}
	if err := readOnly.Put(context.Background(), sealedRecordWithEntropy(t, 2)); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("read-only Put error = %v, want ErrEvidenceInvalid", err)
	}
}

func TestStoreOpenRejectsCaseAliasedRootAndRunsEntries(t *testing.T) {
	tests := []struct {
		name          string
		canonicalRoot func(string) string
		actualRuns    func(string) string
	}{
		{
			name:          "evidence root",
			canonicalRoot: func(parent string) string { return filepath.Join(parent, "evidence") },
			actualRuns:    func(parent string) string { return filepath.Join(parent, "EVIDENCE", "runs") },
		},
		{
			name:          "runs directory",
			canonicalRoot: func(parent string) string { return filepath.Join(parent, "evidence") },
			actualRuns:    func(parent string) string { return filepath.Join(parent, "evidence", "RUNS") },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := safeEvidenceTempDir(t)
			canonicalRoot := test.canonicalRoot(parent)
			if err := os.MkdirAll(test.actualRuns(parent), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(canonicalRoot, "runs")); errors.Is(err, fs.ErrNotExist) {
				t.Skip("filesystem treats case variants as distinct entries")
			} else if err != nil {
				t.Fatalf("Lstat canonical alias: %v", err)
			}
			for name, open := range map[string]func(string) (*Store, error){
				"writable":  NewStore,
				"read-only": OpenStoreReadOnly,
			} {
				if _, err := open(canonicalRoot); !errors.Is(err, ErrEvidenceUnsafePath) {
					t.Fatalf("%s open error = %v, want ErrEvidenceUnsafePath", name, err)
				}
			}
		})
	}
}

func TestStoreGetClassifiesMissingUnsafeOversizeAndCorruptStorage(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		store := newTestStore(t)
		if _, err := store.Get(context.Background(), attemptID(1)); !errors.Is(err, ErrEvidenceNotFound) {
			t.Fatalf("error = %v, want ErrEvidenceNotFound", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		store := newTestStore(t)
		outside := filepath.Join(filepath.Dir(store.root), "outside")
		if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
			t.Fatalf("write outside: %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(store.runs, attemptID(1)+".json")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := store.Get(context.Background(), attemptID(1)); !errors.Is(err, ErrEvidenceUnsafePath) {
			t.Fatalf("error = %v, want ErrEvidenceUnsafePath", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		store := newTestStore(t)
		if err := os.Mkdir(filepath.Join(store.runs, attemptID(1)+".json"), 0o755); err != nil {
			t.Fatalf("mkdir record: %v", err)
		}
		if _, err := store.Get(context.Background(), attemptID(1)); !errors.Is(err, ErrEvidenceCorrupt) {
			t.Fatalf("error = %v, want ErrEvidenceCorrupt", err)
		}
	})

	t.Run("oversize", func(t *testing.T) {
		store := newTestStore(t)
		path := filepath.Join(store.runs, attemptID(1)+".json")
		if err := os.WriteFile(path, make([]byte, MaxRecordBytes+1), 0o644); err != nil {
			t.Fatalf("write oversize: %v", err)
		}
		if _, err := store.Get(context.Background(), attemptID(1)); !errors.Is(err, ErrEvidenceTooLarge) {
			t.Fatalf("error = %v, want ErrEvidenceTooLarge", err)
		}
	})

	t.Run("corrupt", func(t *testing.T) {
		store := newTestStore(t)
		if err := os.WriteFile(filepath.Join(store.runs, attemptID(1)+".json"), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write corrupt: %v", err)
		}
		if _, err := store.Get(context.Background(), attemptID(1)); !errors.Is(err, ErrEvidenceCorrupt) {
			t.Fatalf("error = %v, want ErrEvidenceCorrupt", err)
		}
	})

	t.Run("filename mismatch", func(t *testing.T) {
		store := newTestStore(t)
		record := sealedRecordWithEntropy(t, 1)
		encoded, err := Encode(record)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if err := os.WriteFile(filepath.Join(store.runs, attemptID(2)+".json"), encoded, 0o644); err != nil {
			t.Fatalf("write mismatched record: %v", err)
		}
		if _, err := store.Get(context.Background(), attemptID(2)); !errors.Is(err, ErrEvidenceCorrupt) {
			t.Fatalf("error = %v, want ErrEvidenceCorrupt", err)
		}
	})
}

func TestStoreListAllowsOnlyGitkeepAndExactRecordNames(t *testing.T) {
	tests := []struct {
		name   string
		create func(*testing.T, *Store)
	}{
		{name: "unknown file", create: func(t *testing.T, store *Store) {
			if err := os.WriteFile(filepath.Join(store.runs, "notes.txt"), []byte("x"), 0o644); err != nil {
				t.Fatalf("write unknown: %v", err)
			}
		}},
		{name: "invalid record name", create: func(t *testing.T, store *Store) {
			if err := os.WriteFile(filepath.Join(store.runs, "run-bad.json"), []byte("x"), 0o644); err != nil {
				t.Fatalf("write invalid name: %v", err)
			}
		}},
		{name: "directory", create: func(t *testing.T, store *Store) {
			if err := os.Mkdir(filepath.Join(store.runs, "nested"), 0o755); err != nil {
				t.Fatalf("mkdir unexpected: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			if err := os.WriteFile(filepath.Join(store.runs, ".gitkeep"), []byte{}, 0o644); err != nil {
				t.Fatalf("write .gitkeep: %v", err)
			}
			test.create(t, store)
			if _, err := store.List(context.Background()); !errors.Is(err, ErrEvidenceCorrupt) {
				t.Fatalf("List error = %v, want ErrEvidenceCorrupt", err)
			}
		})
	}

	store := newTestStore(t)
	if err := os.WriteFile(filepath.Join(store.runs, ".gitkeep"), []byte{}, 0o644); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}
	records, err := store.List(context.Background())
	if err != nil || len(records) != 0 {
		t.Fatalf("List with only .gitkeep = %v, %v", records, err)
	}
}

func TestStoreListRejectsMaliciousGitkeepEntries(t *testing.T) {
	tests := []struct {
		name      string
		create    func(*testing.T, string)
		wantError error
	}{
		{name: "nonempty", wantError: ErrEvidenceCorrupt, create: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("not a marker"), 0o644); err != nil {
				t.Fatalf("write marker: %v", err)
			}
		}},
		{name: "directory", wantError: ErrEvidenceCorrupt, create: func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatalf("mkdir marker: %v", err)
			}
		}},
		{name: "symlink", wantError: ErrEvidenceUnsafePath, create: func(t *testing.T, path string) {
			outside := filepath.Join(filepath.Dir(filepath.Dir(path)), "outside-marker")
			if err := os.WriteFile(outside, []byte{}, 0o644); err != nil {
				t.Fatalf("write outside marker: %v", err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			test.create(t, filepath.Join(store.runs, ".gitkeep"))
			if _, err := store.List(context.Background()); !errors.Is(err, test.wantError) {
				t.Fatalf("List error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestStorePutClassifiesOperationalFailureAsIOAndPreservesStage(t *testing.T) {
	store := newTestStore(t)
	injected := errors.New("injected sync failure")
	stageError := &immutablefile.Error{Stage: immutablefile.StageSyncFile, Installed: false, Err: injected}
	store.writeNoReplace = func(context.Context, string, fs.FileInfo, []byte) (immutablefile.Result, error) {
		return immutablefile.Result{}, stageError
	}
	err := store.Put(context.Background(), sealedRecordWithEntropy(t, 1))
	if !errors.Is(err, ErrEvidenceIO) || errors.Is(err, ErrEvidenceCorrupt) || !errors.Is(err, injected) {
		t.Fatalf("Put error = %v, want ErrEvidenceIO wrapping injected error", err)
	}
	var gotStage *immutablefile.Error
	if !errors.As(err, &gotStage) || gotStage.Stage != immutablefile.StageSyncFile || gotStage.Installed {
		t.Fatalf("Put error lost immutable stage: %#v", gotStage)
	}
}

func TestStoreHonorsCancellationWithoutInstalling(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	record := sealedRecordWithEntropy(t, 1)
	if err := store.Put(ctx, record); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put canceled error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(store.runs, record.ID+".json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("canceled Put installed record: %v", err)
	}
	if _, err := store.Get(ctx, record.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get canceled error = %v", err)
	}
	if _, err := store.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("List canceled error = %v", err)
	}
}

func TestIndependentStoresHaveExactlyOneConcurrentPutWinner(t *testing.T) {
	parent := safeEvidenceTempDir(t)
	root := filepath.Join(parent, "evidence")
	const writers = 24
	stores := make([]*Store, writers)
	for index := range stores {
		store, err := NewStore(root)
		if err != nil {
			t.Fatalf("NewStore %d: %v", index, err)
		}
		stores[index] = store
	}
	record := sealedRecordWithEntropy(t, 1)
	start := make(chan struct{})
	var wait sync.WaitGroup
	var installed atomic.Int64
	var conflicts atomic.Int64
	var unexpected atomic.Value
	for _, store := range stores {
		wait.Add(1)
		go func(store *Store) {
			defer wait.Done()
			<-start
			err := store.Put(context.Background(), record)
			switch {
			case err == nil:
				installed.Add(1)
			case errors.Is(err, ErrEvidenceExists):
				conflicts.Add(1)
			default:
				unexpected.Store(fmt.Errorf("unexpected Put error: %w", err))
			}
		}(store)
	}
	close(start)
	wait.Wait()
	if value := unexpected.Load(); value != nil {
		t.Fatal(value)
	}
	if installed.Load() != 1 || conflicts.Load() != writers-1 {
		t.Fatalf("installed=%d conflicts=%d", installed.Load(), conflicts.Load())
	}
}

func TestStoreRemainsBoundToRunsDirectoryIdentityCapturedByNewStore(t *testing.T) {
	operations := []struct {
		name string
		run  func(*Store, Record) error
	}{
		{name: "Put", run: func(store *Store, record Record) error { return store.Put(context.Background(), record) }},
		{name: "Get", run: func(store *Store, record Record) error {
			_, err := store.Get(context.Background(), record.ID)
			return err
		}},
		{name: "List", run: func(store *Store, _ Record) error {
			_, err := store.List(context.Background())
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			store := newTestStore(t)
			record := sealedRecordWithEntropy(t, 1)
			if err := store.Put(context.Background(), record); err != nil {
				t.Fatalf("initial Put: %v", err)
			}
			moved := store.runs + ".old"
			if err := os.Rename(store.runs, moved); err != nil {
				t.Fatalf("rename runs: %v", err)
			}
			if err := os.Mkdir(store.runs, 0o755); err != nil {
				t.Fatalf("replace runs: %v", err)
			}
			if err := operation.run(store, record); !errors.Is(err, ErrEvidenceUnsafePath) {
				t.Fatalf("operation error = %v, want ErrEvidenceUnsafePath", err)
			}
		})
	}
}

func TestStorePutPassesFrozenRunsIdentityIntoImmutableWriter(t *testing.T) {
	store := newTestStore(t)
	record := sealedRecordWithEntropy(t, 1)
	store.writeNoReplace = func(ctx context.Context, destination string, expected fs.FileInfo, data []byte) (immutablefile.Result, error) {
		moved := store.runs + ".old"
		if err := os.Rename(store.runs, moved); err != nil {
			t.Fatalf("rename runs: %v", err)
		}
		if err := os.Mkdir(store.runs, 0o755); err != nil {
			t.Fatalf("replace runs: %v", err)
		}
		return immutablefile.WriteNoReplaceExpected(ctx, destination, expected, data)
	}
	if err := store.Put(context.Background(), record); !errors.Is(err, ErrEvidenceUnsafePath) {
		t.Fatalf("Put error = %v, want ErrEvidenceUnsafePath", err)
	}
	if _, err := os.Lstat(filepath.Join(store.runs, record.ID+".json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("replacement runs received record: %v", err)
	}
}

func TestStoreListUsesOneRunsAnchorAndRejectsMidOperationSwap(t *testing.T) {
	store := newTestStore(t)
	original := sealedRecordWithEntropy(t, 1)
	if err := store.Put(context.Background(), original); err != nil {
		t.Fatalf("Put original: %v", err)
	}
	originalRead := store.readOps.readDirectory
	store.readOps.readDirectory = func(directory *os.File) ([]fs.DirEntry, error) {
		entries, err := originalRead(directory)
		if err != nil {
			return nil, err
		}
		moved := store.runs + ".old"
		if err := os.Rename(store.runs, moved); err != nil {
			t.Fatalf("rename runs: %v", err)
		}
		if err := os.Mkdir(store.runs, 0o755); err != nil {
			t.Fatalf("replace runs: %v", err)
		}
		replacementSource := validRecord()
		replacementSource.ID = original.ID
		replacementSource.Conclusion = "replacement directory record"
		replacement, err := Seal(replacementSource)
		if err != nil {
			t.Fatalf("Seal replacement: %v", err)
		}
		encoded, err := Encode(replacement)
		if err != nil {
			t.Fatalf("Encode replacement: %v", err)
		}
		if err := os.WriteFile(filepath.Join(store.runs, original.ID+".json"), encoded, 0o644); err != nil {
			t.Fatalf("write replacement: %v", err)
		}
		return entries, nil
	}
	records, err := store.List(context.Background())
	if !errors.Is(err, ErrEvidenceUnsafePath) || records != nil {
		t.Fatalf("List = %#v, %v; want nil ErrEvidenceUnsafePath", records, err)
	}
}

func TestStoreListClassifiesCandidateDisappearanceInsideAnchorAsCorrupt(t *testing.T) {
	store := newTestStore(t)
	record := sealedRecordWithEntropy(t, 1)
	if err := store.Put(context.Background(), record); err != nil {
		t.Fatalf("Put: %v", err)
	}
	originalRead := store.readOps.readDirectory
	store.readOps.readDirectory = func(directory *os.File) ([]fs.DirEntry, error) {
		entries, err := originalRead(directory)
		if err != nil {
			return nil, err
		}
		if err := os.Remove(filepath.Join(store.runs, record.ID+".json")); err != nil {
			t.Fatalf("remove candidate: %v", err)
		}
		return entries, nil
	}
	if _, err := store.List(context.Background()); !errors.Is(err, ErrEvidenceCorrupt) || errors.Is(err, ErrEvidenceNotFound) {
		t.Fatalf("List error = %v, want corrupt and not not-found", err)
	}
}

func TestStoreRejectsOversizedPutAndExistingNonregularTargets(t *testing.T) {
	store := newTestStore(t)
	oversize := validRecord()
	oversize.ID = attemptID(1)
	oversize.Conclusion = string(make([]byte, MaxRecordBytes))
	sealed, err := Seal(oversize)
	if err != nil {
		t.Fatalf("Seal oversize: %v", err)
	}
	if err := store.Put(context.Background(), sealed); !errors.Is(err, ErrEvidenceTooLarge) {
		t.Fatalf("oversize Put error = %v", err)
	}

	for index, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			record := sealedRecordWithEntropy(t, index+10)
			target := filepath.Join(store.runs, record.ID+".json")
			if kind == "directory" {
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatalf("mkdir target: %v", err)
				}
			} else {
				outside := filepath.Join(filepath.Dir(store.root), fmt.Sprintf("outside-%d", index))
				if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
					t.Fatalf("write outside: %v", err)
				}
				if err := os.Symlink(outside, target); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			if err := store.Put(context.Background(), record); !errors.Is(err, ErrEvidenceExists) {
				t.Fatalf("Put existing %s error = %v", kind, err)
			}
		})
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	root := filepath.Join(safeEvidenceTempDir(t), "evidence")
	store, err := NewStore(root)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func safeEvidenceTempDir(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	return directory
}

func sealedRecordWithEntropy(t *testing.T, entropy int) Record {
	t.Helper()
	record := validRecord()
	record.ID = attemptID(entropy)
	sealed, err := Seal(record)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return sealed
}

func attemptID(entropy int) string {
	return fmt.Sprintf("run-20260714T120000.123Z-%032x", entropy)
}

type alteredFileInfo struct {
	fs.FileInfo
	sizeDelta    int64
	modTimeDelta time.Duration
}

func (info alteredFileInfo) Size() int64 {
	return info.FileInfo.Size() + info.sizeDelta
}

func (info alteredFileInfo) ModTime() time.Time {
	return info.FileInfo.ModTime().Add(info.modTimeDelta)
}
