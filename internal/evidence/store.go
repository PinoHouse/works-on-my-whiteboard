package evidence

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/immutablefile"
)

var (
	ErrEvidenceExists     = errors.New("evidence already exists")
	ErrEvidenceNotFound   = errors.New("evidence not found")
	ErrEvidenceInvalid    = errors.New("invalid evidence input")
	ErrEvidenceTooLarge   = errors.New("evidence exceeds size limit")
	ErrEvidenceUnsafePath = errors.New("unsafe evidence path")
	ErrEvidenceCorrupt    = errors.New("corrupt evidence storage")
	ErrEvidenceIO         = errors.New("evidence I/O failure")
)

type immutableWriteFunc func(context.Context, string, fs.FileInfo, []byte) (immutablefile.Result, error)

type evidenceReadOperations struct {
	lstat          func(*os.Root, string) (fs.FileInfo, error)
	openFile       func(*os.Root, string) (*os.File, error)
	statFile       func(*os.File) (fs.FileInfo, error)
	readFile       func(*os.File, int64) ([]byte, error)
	closeFile      func(*os.File) error
	openDirectory  func(*os.Root) (*os.File, error)
	readDirectory  func(*os.File) ([]fs.DirEntry, error)
	closeDirectory func(*os.File) error
}

type evidenceAnchorOperations struct {
	openRoot      func(string) (*os.Root, error)
	lstat         func(*os.Root, string) (fs.FileInfo, error)
	openChild     func(*os.Root, string) (*os.Root, error)
	openDirectory func(*os.Root) (*os.File, error)
	readDirectory func(*os.File) ([]fs.DirEntry, error)
	statFile      func(*os.File) (fs.FileInfo, error)
	closeFile     func(*os.File) error
	closeRoot     func(*os.Root) error
}

func defaultEvidenceAnchorOperations() evidenceAnchorOperations {
	return evidenceAnchorOperations{
		openRoot: os.OpenRoot,
		lstat: func(root *os.Root, name string) (fs.FileInfo, error) {
			return root.Lstat(name)
		},
		openChild: func(root *os.Root, name string) (*os.Root, error) {
			return root.OpenRoot(name)
		},
		openDirectory: func(root *os.Root) (*os.File, error) {
			return root.Open(".")
		},
		readDirectory: func(directory *os.File) ([]fs.DirEntry, error) {
			return directory.ReadDir(-1)
		},
		statFile: func(file *os.File) (fs.FileInfo, error) {
			return file.Stat()
		},
		closeFile: func(file *os.File) error {
			return file.Close()
		},
		closeRoot: func(root *os.Root) error {
			return root.Close()
		},
	}
}

func (operations evidenceAnchorOperations) withDefaults() evidenceAnchorOperations {
	defaults := defaultEvidenceAnchorOperations()
	if operations.openRoot == nil {
		operations.openRoot = defaults.openRoot
	}
	if operations.lstat == nil {
		operations.lstat = defaults.lstat
	}
	if operations.openChild == nil {
		operations.openChild = defaults.openChild
	}
	if operations.openDirectory == nil {
		operations.openDirectory = defaults.openDirectory
	}
	if operations.readDirectory == nil {
		operations.readDirectory = defaults.readDirectory
	}
	if operations.statFile == nil {
		operations.statFile = defaults.statFile
	}
	if operations.closeFile == nil {
		operations.closeFile = defaults.closeFile
	}
	if operations.closeRoot == nil {
		operations.closeRoot = defaults.closeRoot
	}
	return operations
}

func defaultEvidenceReadOperations() evidenceReadOperations {
	return evidenceReadOperations{
		lstat: func(root *os.Root, name string) (fs.FileInfo, error) {
			return root.Lstat(name)
		},
		openFile: func(root *os.Root, name string) (*os.File, error) {
			return root.Open(name)
		},
		statFile: func(file *os.File) (fs.FileInfo, error) {
			return file.Stat()
		},
		readFile: func(file *os.File, limit int64) ([]byte, error) {
			return io.ReadAll(io.LimitReader(file, limit))
		},
		closeFile: func(file *os.File) error {
			return file.Close()
		},
		openDirectory: func(root *os.Root) (*os.File, error) {
			return root.Open(".")
		},
		readDirectory: func(directory *os.File) ([]fs.DirEntry, error) {
			return directory.ReadDir(-1)
		},
		closeDirectory: func(directory *os.File) error {
			return directory.Close()
		},
	}
}

func (operations evidenceReadOperations) withDefaults() evidenceReadOperations {
	defaults := defaultEvidenceReadOperations()
	if operations.lstat == nil {
		operations.lstat = defaults.lstat
	}
	if operations.openFile == nil {
		operations.openFile = defaults.openFile
	}
	if operations.statFile == nil {
		operations.statFile = defaults.statFile
	}
	if operations.readFile == nil {
		operations.readFile = defaults.readFile
	}
	if operations.closeFile == nil {
		operations.closeFile = defaults.closeFile
	}
	if operations.openDirectory == nil {
		operations.openDirectory = defaults.openDirectory
	}
	if operations.readDirectory == nil {
		operations.readDirectory = defaults.readDirectory
	}
	if operations.closeDirectory == nil {
		operations.closeDirectory = defaults.closeDirectory
	}
	return operations
}

type Store struct {
	root           string
	runs           string
	runsIdentity   fs.FileInfo
	writable       bool
	writeNoReplace immutableWriteFunc
	readOps        evidenceReadOperations
	anchorOps      evidenceAnchorOperations
}

func NewStore(root string) (*Store, error) {
	absolute, err := resolveEvidenceStoreRoot(root)
	if err != nil {
		return nil, err
	}
	if err := prepareRealDirectory(absolute); err != nil {
		return nil, err
	}
	runs := filepath.Join(absolute, "runs")
	if err := prepareRealDirectory(runs); err != nil {
		return nil, err
	}
	return openExistingStore(absolute, true, ErrEvidenceUnsafePath)
}

// OpenStoreReadOnly opens an existing evidence store without creating its root
// or runs directory. Missing directory components are reported as
// ErrEvidenceNotFound, while aliases and replacements remain unsafe.
func OpenStoreReadOnly(root string) (*Store, error) {
	absolute, err := resolveEvidenceStoreRoot(root)
	if err != nil {
		return nil, err
	}
	return openExistingStore(absolute, false, ErrEvidenceNotFound)
}

func resolveEvidenceStoreRoot(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("%w: root is empty", ErrEvidenceInvalid)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve root: %w", ErrEvidenceInvalid, err)
	}
	absolute = filepath.Clean(absolute)
	if absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return "", fmt.Errorf("%w: filesystem root cannot be an evidence root", ErrEvidenceUnsafePath)
	}
	return absolute, nil
}

func openExistingStore(absolute string, writable bool, missingCategory error) (*Store, error) {
	runs := filepath.Join(absolute, "runs")
	anchorOps := defaultEvidenceAnchorOperations()
	runsRoot, err := openRealDirectoryWithMissingCategory(runs, anchorOps, missingCategory)
	if err != nil {
		return nil, err
	}
	runsIdentity, infoErr := rootDirectoryInfoWithOperations(runsRoot, anchorOps)
	closeErr := anchorOps.closeRoot(runsRoot)
	if infoErr != nil || closeErr != nil {
		return nil, errors.Join(infoErr, wrapEvidenceIO("close captured runs directory identity", closeErr))
	}
	return &Store{
		root:         absolute,
		runs:         runs,
		runsIdentity: runsIdentity,
		writable:     writable,
		writeNoReplace: func(ctx context.Context, destination string, expected fs.FileInfo, data []byte) (immutablefile.Result, error) {
			return immutablefile.WriteNoReplaceExpected(ctx, destination, expected, data)
		},
		readOps:   defaultEvidenceReadOperations(),
		anchorOps: anchorOps,
	}, nil
}

func (store *Store) Put(ctx context.Context, record Record) error {
	if err := validateEvidenceID(record.ID); err != nil {
		return err
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrEvidenceInvalid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := Encode(record)
	if err != nil {
		return fmt.Errorf("%w: record is not a valid sealed value: %w", ErrEvidenceInvalid, err)
	}
	if len(encoded) > MaxRecordBytes {
		return fmt.Errorf("%w: %d bytes", ErrEvidenceTooLarge, len(encoded))
	}
	if store == nil || store.runs == "" || store.runsIdentity == nil {
		return fmt.Errorf("%w: store is not initialized", ErrEvidenceUnsafePath)
	}
	if !store.writable {
		return fmt.Errorf("%w: store is read-only", ErrEvidenceInvalid)
	}
	destination := filepath.Join(store.runs, record.ID+".json")
	writeNoReplace := store.writeNoReplace
	if writeNoReplace == nil {
		writeNoReplace = func(ctx context.Context, destination string, expected fs.FileInfo, data []byte) (immutablefile.Result, error) {
			return immutablefile.WriteNoReplaceExpected(ctx, destination, expected, data)
		}
	}
	_, err = writeNoReplace(ctx, destination, store.runsIdentity, encoded)
	var operationErr error
	switch {
	case err == nil:
	case errors.Is(err, immutablefile.ErrExists):
		operationErr = fmt.Errorf("%w: %s: %w", ErrEvidenceExists, record.ID, err)
	case errors.Is(err, immutablefile.ErrUnsafePath):
		operationErr = fmt.Errorf("%w: %s: %w", ErrEvidenceUnsafePath, record.ID, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		operationErr = err
	default:
		operationErr = fmt.Errorf("%w: put %s: %w", ErrEvidenceIO, record.ID, err)
	}
	return store.finishRunsOperation(operationErr)
}

func (store *Store) Get(ctx context.Context, id string) (Record, error) {
	if err := validateEvidenceID(id); err != nil {
		return Record{}, err
	}
	if ctx == nil {
		return Record{}, fmt.Errorf("%w: context is nil", ErrEvidenceInvalid)
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	root, err := store.openRuns()
	if err != nil {
		return Record{}, err
	}
	readOps := store.readOps.withDefaults()
	record, operationErr := getFromRoot(ctx, root, id, ErrEvidenceNotFound, readOps)
	if closeErr := store.anchorOps.withDefaults().closeRoot(root); closeErr != nil {
		operationErr = preferEvidenceIO(operationErr, "close runs anchor", closeErr)
	}
	if err := store.finishRunsOperation(operationErr); err != nil {
		return Record{}, err
	}
	return record, nil
}

func getFromRoot(ctx context.Context, root *os.Root, id string, missingCategory error, readOps evidenceReadOperations) (Record, error) {
	name := id + ".json"
	expected, err := readOps.lstat(root, name)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, fmt.Errorf("%w: %s", missingCategory, id)
	}
	if err != nil {
		return Record{}, evidenceIO("inspect record "+id, err)
	}
	if expected.Mode()&fs.ModeSymlink != 0 {
		return Record{}, fmt.Errorf("%w: record %s is a symlink", ErrEvidenceUnsafePath, id)
	}
	if !expected.Mode().IsRegular() {
		return Record{}, fmt.Errorf("%w: record %s is not regular", ErrEvidenceCorrupt, id)
	}
	if expected.Size() > MaxRecordBytes {
		return Record{}, fmt.Errorf("%w: record %s is %d bytes", ErrEvidenceTooLarge, id, expected.Size())
	}

	file, err := readOps.openFile(root, name)
	if err != nil {
		return Record{}, classifyOpenFailure(root, name, "record "+id, expected, err, readOps)
	}
	opened, err := readOps.statFile(file)
	if err != nil {
		operationErr := evidenceIO("stat open record "+id, err)
		return Record{}, closeEvidenceFile(file, "record "+id, operationErr, readOps)
	}
	current, currentErr := readOps.lstat(root, name)
	if operationErr := classifySnapshot(expected, current, currentErr, "record "+id, "while opening"); operationErr != nil {
		return Record{}, closeEvidenceFile(file, "record "+id, operationErr, readOps)
	}
	if !sameRegularSnapshot(expected, opened) {
		operationErr := fmt.Errorf("%w: record %s changed while opening", ErrEvidenceCorrupt, id)
		return Record{}, closeEvidenceFile(file, "record "+id, operationErr, readOps)
	}
	data, err := readOps.readFile(file, MaxRecordBytes+1)
	if err != nil {
		operationErr := evidenceIO("read record "+id, err)
		return Record{}, closeEvidenceFile(file, "record "+id, operationErr, readOps)
	}
	afterRead, statErr := readOps.statFile(file)
	closeErr := readOps.closeFile(file)
	afterPath, pathErr := readOps.lstat(root, name)
	operational := make([]error, 0, 3)
	if statErr != nil {
		operational = append(operational, fmt.Errorf("stat record after read: %w", statErr))
	}
	if closeErr != nil {
		operational = append(operational, fmt.Errorf("close record after read: %w", closeErr))
	}
	if pathErr != nil && !errors.Is(pathErr, fs.ErrNotExist) {
		operational = append(operational, fmt.Errorf("inspect record after read: %w", pathErr))
	}
	if len(operational) != 0 {
		return Record{}, evidenceIO("finish reading record "+id, errors.Join(operational...))
	}
	if operationErr := classifySnapshot(expected, afterPath, pathErr, "record "+id, "after reading"); operationErr != nil {
		return Record{}, operationErr
	}
	if !sameRegularSnapshot(expected, afterRead) || int64(len(data)) != expected.Size() {
		return Record{}, fmt.Errorf("%w: record %s changed while reading", ErrEvidenceCorrupt, id)
	}
	if len(data) > MaxRecordBytes {
		return Record{}, fmt.Errorf("%w: record %s exceeded read limit", ErrEvidenceTooLarge, id)
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	decoded, err := Decode(data)
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			return Record{}, fmt.Errorf("%w: record %s: %w", ErrEvidenceTooLarge, id, err)
		}
		return Record{}, fmt.Errorf("%w: record %s: %w", ErrEvidenceCorrupt, id, err)
	}
	if decoded.ID != id {
		return Record{}, fmt.Errorf("%w: filename ID %s contains record %s", ErrEvidenceCorrupt, id, decoded.ID)
	}
	return decoded, nil
}

func evidenceIO(operation string, cause error) error {
	return fmt.Errorf("%w: %s: %w", ErrEvidenceIO, operation, cause)
}

func wrapEvidenceIO(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	return evidenceIO(operation, cause)
}

func preferEvidenceIO(prior error, operation string, cause error) error {
	if prior == nil {
		return evidenceIO(operation, cause)
	}
	if errors.Is(prior, ErrEvidenceIO) {
		return evidenceIO(operation, errors.Join(prior, cause))
	}
	return fmt.Errorf("%w: %s: %w; prior semantic result: %v", ErrEvidenceIO, operation, cause, prior)
}

func closeEvidenceFile(file *os.File, label string, prior error, readOps evidenceReadOperations) error {
	if closeErr := readOps.closeFile(file); closeErr != nil {
		return preferEvidenceIO(prior, "close "+label, closeErr)
	}
	return prior
}

func classifyOpenFailure(root *os.Root, name, label string, expected fs.FileInfo, openErr error, readOps evidenceReadOperations) error {
	current, currentErr := readOps.lstat(root, name)
	if currentErr != nil && !errors.Is(currentErr, fs.ErrNotExist) {
		return evidenceIO("open and re-inspect "+label, errors.Join(openErr, currentErr))
	}
	if operationErr := classifySnapshot(expected, current, currentErr, label, "while opening"); operationErr != nil {
		return errors.Join(operationErr, fmt.Errorf("open %s: %w", label, openErr))
	}
	return evidenceIO("open "+label, openErr)
}

func classifySnapshot(expected, current fs.FileInfo, currentErr error, label, phase string) error {
	if errors.Is(currentErr, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s disappeared %s", ErrEvidenceCorrupt, label, phase)
	}
	if currentErr != nil {
		return evidenceIO("inspect "+label+" "+phase, currentErr)
	}
	if current.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s became a symlink %s", ErrEvidenceUnsafePath, label, phase)
	}
	if !sameRegularSnapshot(expected, current) {
		return fmt.Errorf("%w: %s changed %s", ErrEvidenceCorrupt, label, phase)
	}
	return nil
}

func (store *Store) List(ctx context.Context) ([]Record, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrEvidenceInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := store.openRuns()
	if err != nil {
		return nil, err
	}
	readOps := store.readOps.withDefaults()
	records, operationErr := store.listFromRoot(ctx, root, readOps)
	if closeErr := store.anchorOps.withDefaults().closeRoot(root); closeErr != nil {
		operationErr = preferEvidenceIO(operationErr, "close runs anchor", closeErr)
	}
	if err := store.finishRunsOperation(operationErr); err != nil {
		return nil, err
	}
	return records, nil
}

func (store *Store) listFromRoot(ctx context.Context, root *os.Root, readOps evidenceReadOperations) ([]Record, error) {
	directory, err := readOps.openDirectory(root)
	if err != nil {
		return nil, evidenceIO("open runs directory", err)
	}
	entries, readErr := readOps.readDirectory(directory)
	closeErr := readOps.closeDirectory(directory)
	if readErr != nil || closeErr != nil {
		causes := make([]error, 0, 2)
		if readErr != nil {
			causes = append(causes, fmt.Errorf("read directory entries: %w", readErr))
		}
		if closeErr != nil {
			causes = append(causes, fmt.Errorf("close directory: %w", closeErr))
		}
		return nil, evidenceIO("read runs directory", errors.Join(causes...))
	}

	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if name == ".gitkeep" {
			if err := validateEmptyMarker(root, name, readOps); err != nil {
				return nil, err
			}
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			return nil, fmt.Errorf("%w: unexpected runs entry %q", ErrEvidenceCorrupt, name)
		}
		id := strings.TrimSuffix(name, ".json")
		if ValidateID(id) != nil || name != id+".json" {
			return nil, fmt.Errorf("%w: invalid evidence filename %q", ErrEvidenceCorrupt, name)
		}
		record, err := getFromRoot(ctx, root, id, ErrEvidenceCorrupt, readOps)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.Slice(records, func(left, right int) bool {
		return lessRecord(records[left], records[right])
	})
	return records, nil
}

func validateEmptyMarker(root *os.Root, name string, readOps evidenceReadOperations) error {
	expected, err := readOps.lstat(root, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s disappeared during listing", ErrEvidenceCorrupt, name)
		}
		return evidenceIO("inspect "+name, err)
	}
	if expected.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symlink", ErrEvidenceUnsafePath, name)
	}
	if !expected.Mode().IsRegular() || expected.Size() != 0 {
		return fmt.Errorf("%w: %s must be a zero-byte regular file", ErrEvidenceCorrupt, name)
	}
	file, err := readOps.openFile(root, name)
	if err != nil {
		return classifyOpenFailure(root, name, name, expected, err, readOps)
	}
	opened, statErr := readOps.statFile(file)
	if statErr != nil {
		return closeEvidenceFile(file, name, evidenceIO("stat open "+name, statErr), readOps)
	}
	current, currentErr := readOps.lstat(root, name)
	if operationErr := classifySnapshot(expected, current, currentErr, name, "while opening"); operationErr != nil {
		return closeEvidenceFile(file, name, operationErr, readOps)
	}
	if !sameRegularSnapshot(expected, opened) {
		return closeEvidenceFile(file, name, fmt.Errorf("%w: %s changed while opening", ErrEvidenceCorrupt, name), readOps)
	}
	data, readErr := readOps.readFile(file, 1)
	if readErr != nil {
		return closeEvidenceFile(file, name, evidenceIO("read "+name, readErr), readOps)
	}
	afterRead, afterErr := readOps.statFile(file)
	closeErr := readOps.closeFile(file)
	afterPath, pathErr := readOps.lstat(root, name)
	operational := make([]error, 0, 3)
	if afterErr != nil {
		operational = append(operational, fmt.Errorf("stat marker after read: %w", afterErr))
	}
	if closeErr != nil {
		operational = append(operational, fmt.Errorf("close marker after read: %w", closeErr))
	}
	if pathErr != nil && !errors.Is(pathErr, fs.ErrNotExist) {
		operational = append(operational, fmt.Errorf("inspect marker after read: %w", pathErr))
	}
	if len(operational) != 0 {
		return evidenceIO("finish reading "+name, errors.Join(operational...))
	}
	if operationErr := classifySnapshot(expected, afterPath, pathErr, name, "after reading"); operationErr != nil {
		return operationErr
	}
	if len(data) != 0 || !sameRegularSnapshot(expected, afterRead) {
		return fmt.Errorf("%w: %s changed while reading", ErrEvidenceCorrupt, name)
	}
	return nil
}

func (store *Store) openRuns() (*os.Root, error) {
	if store == nil || store.runs == "" || store.runsIdentity == nil {
		return nil, fmt.Errorf("%w: store is not initialized", ErrEvidenceUnsafePath)
	}
	anchorOps := store.anchorOps.withDefaults()
	root, err := openRealDirectoryWithOperations(store.runs, anchorOps)
	if err != nil {
		return nil, err
	}
	openedIdentity, err := rootDirectoryInfoWithOperations(root, anchorOps)
	if err != nil {
		return nil, closeEvidenceRoots(err, anchorOps, root)
	}
	if !os.SameFile(store.runsIdentity, openedIdentity) {
		identityErr := fmt.Errorf("%w: runs directory identity differs from NewStore", ErrEvidenceUnsafePath)
		return nil, closeEvidenceRoots(identityErr, anchorOps, root)
	}
	return root, nil
}

func (store *Store) finishRunsOperation(operationErr error) error {
	root, identityErr := store.openRuns()
	if identityErr == nil {
		if closeErr := store.anchorOps.withDefaults().closeRoot(root); closeErr != nil {
			return preferEvidenceIO(operationErr, "close final runs identity anchor", closeErr)
		}
	}
	if identityErr != nil {
		if errors.Is(identityErr, ErrEvidenceIO) {
			return preferEvidenceIO(operationErr, "reopen final runs identity anchor", identityErr)
		}
		return errors.Join(identityErr, operationErr)
	}
	return operationErr
}

func rootDirectoryInfo(root *os.Root) (fs.FileInfo, error) {
	return rootDirectoryInfoWithOperations(root, defaultEvidenceAnchorOperations())
}

func rootDirectoryInfoWithOperations(root *os.Root, operations evidenceAnchorOperations) (fs.FileInfo, error) {
	operations = operations.withDefaults()
	opened, err := operations.openDirectory(root)
	if err != nil {
		return nil, evidenceIO("open anchored root directory", err)
	}
	info, statErr := operations.statFile(opened)
	closeErr := operations.closeFile(opened)
	if statErr != nil || closeErr != nil {
		return nil, evidenceIO("inspect anchored root directory", errors.Join(statErr, closeErr))
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: anchored root is not a directory", ErrEvidenceUnsafePath)
	}
	return info, nil
}

func validateEvidenceID(id string) error {
	if err := ValidateID(id); err != nil {
		return fmt.Errorf("%w: invalid evidence ID: %w", ErrEvidenceInvalid, err)
	}
	return nil
}

func lessRecord(left, right Record) bool {
	leftKey := left.CellKey()
	rightKey := right.CellKey()
	leftValues := [...]string{leftKey.LabID, leftKey.RequiredRunID, leftKey.BindingID, leftKey.ClaimID, leftKey.ImplementationID, leftKey.AdapterID, left.ID}
	rightValues := [...]string{rightKey.LabID, rightKey.RequiredRunID, rightKey.BindingID, rightKey.ClaimID, rightKey.ImplementationID, rightKey.AdapterID, right.ID}
	for index := range leftValues {
		if leftValues[index] != rightValues[index] {
			return leftValues[index] < rightValues[index]
		}
	}
	return false
}

func prepareRealDirectory(path string) error {
	operations := defaultEvidenceAnchorOperations()
	components, volumeRoot, err := absolutePathComponents(path)
	if err != nil {
		return err
	}
	currentRoot, err := operations.openRoot(volumeRoot)
	if err != nil {
		return evidenceIO("anchor filesystem root", err)
	}
	currentPath := volumeRoot
	exactStart := len(components) - 2
	if exactStart < 0 {
		exactStart = 0
	}
	for index, component := range components {
		currentPath = filepath.Join(currentPath, component)
		info, lstatErr := operations.lstat(currentRoot, component)
		if errors.Is(lstatErr, fs.ErrNotExist) {
			mkdirErr := currentRoot.Mkdir(component, 0o755)
			if mkdirErr != nil && !errors.Is(mkdirErr, fs.ErrExist) {
				return closeEvidenceRoots(evidenceIO("create directory "+currentPath, mkdirErr), operations, currentRoot)
			}
			info, lstatErr = operations.lstat(currentRoot, component)
		}
		if lstatErr != nil {
			if errors.Is(lstatErr, fs.ErrNotExist) {
				return closeEvidenceRoots(fmt.Errorf("%w: directory %s disappeared while preparing", ErrEvidenceUnsafePath, currentPath), operations, currentRoot)
			}
			return closeEvidenceRoots(evidenceIO("inspect directory "+currentPath, lstatErr), operations, currentRoot)
		}
		if index >= exactStart {
			if exactErr := requireExactEvidenceEntryName(currentRoot, currentPath, component, operations); exactErr != nil {
				return closeEvidenceRoots(exactErr, operations, currentRoot)
			}
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return closeEvidenceRoots(fmt.Errorf("%w: component %s is not a real directory", ErrEvidenceUnsafePath, currentPath), operations, currentRoot)
		}
		childRoot, openErr := operations.openChild(currentRoot, component)
		if openErr != nil {
			classified := classifyAnchorOpenFailure(currentRoot, component, currentPath, info, openErr, operations)
			return closeEvidenceRoots(classified, operations, currentRoot)
		}
		if verifyErr := verifyAnchoredChildWithOperations(currentRoot, childRoot, component, currentPath, info, operations); verifyErr != nil {
			return closeEvidenceRoots(verifyErr, operations, childRoot, currentRoot)
		}
		if closeErr := operations.closeRoot(currentRoot); closeErr != nil {
			return closeEvidenceRoots(evidenceIO("close parent anchor for "+currentPath, closeErr), operations, childRoot)
		}
		currentRoot = childRoot
	}
	if err := operations.closeRoot(currentRoot); err != nil {
		return evidenceIO("close prepared directory anchor", err)
	}
	return nil
}

func openRealDirectory(path string) (*os.Root, error) {
	return openRealDirectoryWithOperations(path, defaultEvidenceAnchorOperations())
}

func openRealDirectoryWithOperations(path string, operations evidenceAnchorOperations) (*os.Root, error) {
	return openRealDirectoryWithMissingCategory(path, operations, ErrEvidenceUnsafePath)
}

func openRealDirectoryWithMissingCategory(path string, operations evidenceAnchorOperations, missingCategory error) (*os.Root, error) {
	operations = operations.withDefaults()
	components, volumeRoot, err := absolutePathComponents(path)
	if err != nil {
		return nil, err
	}
	currentRoot, err := operations.openRoot(volumeRoot)
	if err != nil {
		return nil, evidenceIO("anchor filesystem root", err)
	}
	currentPath := volumeRoot
	exactStart := len(components) - 2
	if exactStart < 0 {
		exactStart = 0
	}
	for index, component := range components {
		currentPath = filepath.Join(currentPath, component)
		info, lstatErr := operations.lstat(currentRoot, component)
		if lstatErr != nil {
			if errors.Is(lstatErr, fs.ErrNotExist) {
				return nil, closeEvidenceRoots(fmt.Errorf("%w: directory %s is missing: %w", missingCategory, currentPath, lstatErr), operations, currentRoot)
			}
			return nil, closeEvidenceRoots(evidenceIO("inspect directory "+currentPath, lstatErr), operations, currentRoot)
		}
		if index >= exactStart {
			if exactErr := requireExactEvidenceEntryName(currentRoot, currentPath, component, operations); exactErr != nil {
				return nil, closeEvidenceRoots(exactErr, operations, currentRoot)
			}
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return nil, closeEvidenceRoots(fmt.Errorf("%w: component %s is not a real directory", ErrEvidenceUnsafePath, currentPath), operations, currentRoot)
		}
		childRoot, openErr := operations.openChild(currentRoot, component)
		if openErr != nil {
			classified := classifyAnchorOpenFailure(currentRoot, component, currentPath, info, openErr, operations)
			return nil, closeEvidenceRoots(classified, operations, currentRoot)
		}
		if verifyErr := verifyAnchoredChildWithOperations(currentRoot, childRoot, component, currentPath, info, operations); verifyErr != nil {
			return nil, closeEvidenceRoots(verifyErr, operations, childRoot, currentRoot)
		}
		if index >= exactStart {
			if exactErr := requireExactEvidenceEntryName(currentRoot, currentPath, component, operations); exactErr != nil {
				return nil, closeEvidenceRoots(exactErr, operations, childRoot, currentRoot)
			}
		}
		if closeErr := operations.closeRoot(currentRoot); closeErr != nil {
			return nil, closeEvidenceRoots(evidenceIO("close parent anchor for "+currentPath, closeErr), operations, childRoot)
		}
		currentRoot = childRoot
	}
	return currentRoot, nil
}

func verifyAnchoredChild(parent, child *os.Root, name string, expected fs.FileInfo) error {
	return verifyAnchoredChildWithOperations(parent, child, name, name, expected, defaultEvidenceAnchorOperations())
}

func verifyAnchoredChildWithOperations(parent, child *os.Root, name, path string, expected fs.FileInfo, operations evidenceAnchorOperations) error {
	operations = operations.withDefaults()
	opened, err := operations.openDirectory(child)
	if err != nil {
		return classifyAnchorOpenFailure(parent, name, path, expected, err, operations)
	}
	openedInfo, infoErr := operations.statFile(opened)
	closeErr := operations.closeFile(opened)
	if infoErr != nil || closeErr != nil {
		return evidenceIO("inspect anchored directory "+path, errors.Join(infoErr, closeErr))
	}
	current, currentErr := operations.lstat(parent, name)
	if currentErr != nil {
		if errors.Is(currentErr, fs.ErrNotExist) {
			return fmt.Errorf("%w: directory component %s disappeared while anchoring", ErrEvidenceUnsafePath, path)
		}
		return evidenceIO("re-inspect directory "+path, currentErr)
	}
	if current.Mode()&fs.ModeSymlink != 0 || !current.IsDir() || !openedInfo.IsDir() || !os.SameFile(expected, current) || !os.SameFile(expected, openedInfo) {
		return fmt.Errorf("%w: directory component %s changed while anchoring", ErrEvidenceUnsafePath, path)
	}
	return nil
}

func requireExactEvidenceEntryName(parent *os.Root, path, expected string, operations evidenceAnchorOperations) error {
	operations = operations.withDefaults()
	directory, err := operations.openDirectory(parent)
	if err != nil {
		return evidenceIO("open parent directory for exact entry "+path, err)
	}
	entries, readErr := operations.readDirectory(directory)
	closeErr := operations.closeFile(directory)
	if readErr != nil || closeErr != nil {
		return evidenceIO("enumerate parent directory for exact entry "+path, errors.Join(readErr, closeErr))
	}
	for _, entry := range entries {
		if entry.Name() == expected {
			return nil
		}
	}
	return fmt.Errorf("%w: directory path %s has no exact entry named %q", ErrEvidenceUnsafePath, path, expected)
}

func classifyAnchorOpenFailure(parent *os.Root, name, path string, expected fs.FileInfo, openErr error, operations evidenceAnchorOperations) error {
	current, currentErr := operations.lstat(parent, name)
	if currentErr != nil {
		if errors.Is(currentErr, fs.ErrNotExist) {
			return fmt.Errorf("%w: directory %s disappeared while opening: %w", ErrEvidenceUnsafePath, path, openErr)
		}
		return evidenceIO("open and re-inspect directory "+path, errors.Join(openErr, currentErr))
	}
	if current.Mode()&fs.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(expected, current) {
		return fmt.Errorf("%w: directory %s changed while opening: %w", ErrEvidenceUnsafePath, path, openErr)
	}
	return evidenceIO("open directory "+path, openErr)
}

func closeEvidenceRoots(primary error, operations evidenceAnchorOperations, roots ...*os.Root) error {
	operations = operations.withDefaults()
	causes := []error{primary}
	for _, root := range roots {
		if root == nil {
			continue
		}
		if closeErr := operations.closeRoot(root); closeErr != nil {
			causes = append(causes, evidenceIO("close directory anchor during cleanup", closeErr))
		}
	}
	return errors.Join(causes...)
}

func absolutePathComponents(path string) ([]string, string, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, "", fmt.Errorf("%w: directory path must be clean and absolute", ErrEvidenceUnsafePath)
	}
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	remainder := strings.TrimPrefix(path, current)
	components := strings.Split(remainder, string(filepath.Separator))
	if len(components) == 1 && components[0] == "" {
		return nil, "", fmt.Errorf("%w: filesystem root is not allowed", ErrEvidenceUnsafePath)
	}
	return components, current, nil
}

func sameRegularSnapshot(left, right fs.FileInfo) bool {
	return left != nil && right != nil && left.Mode().IsRegular() && right.Mode().IsRegular() &&
		os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}
