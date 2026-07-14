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
	writeNoReplace immutableWriteFunc
	readOps        evidenceReadOperations
}

func NewStore(root string) (*Store, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: root is empty", ErrEvidenceInvalid)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve root: %w", ErrEvidenceInvalid, err)
	}
	absolute = filepath.Clean(absolute)
	if absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return nil, fmt.Errorf("%w: filesystem root cannot be an evidence root", ErrEvidenceUnsafePath)
	}
	if err := prepareRealDirectory(absolute); err != nil {
		return nil, err
	}
	runs := filepath.Join(absolute, "runs")
	if err := prepareRealDirectory(runs); err != nil {
		return nil, err
	}
	runsRoot, err := openRealDirectory(runs)
	if err != nil {
		return nil, err
	}
	runsIdentity, infoErr := rootDirectoryInfo(runsRoot)
	closeErr := runsRoot.Close()
	if infoErr != nil || closeErr != nil {
		return nil, fmt.Errorf("%w: capture runs directory identity", ErrEvidenceUnsafePath)
	}
	return &Store{
		root:         absolute,
		runs:         runs,
		runsIdentity: runsIdentity,
		writeNoReplace: func(ctx context.Context, destination string, expected fs.FileInfo, data []byte) (immutablefile.Result, error) {
			return immutablefile.WriteNoReplaceExpected(ctx, destination, expected, data)
		},
		readOps: defaultEvidenceReadOperations(),
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
	if closeErr := root.Close(); closeErr != nil {
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
		return operationErr
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
	if closeErr := root.Close(); closeErr != nil {
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
	root, err := openRealDirectory(store.runs)
	if err != nil {
		return nil, err
	}
	openedIdentity, err := rootDirectoryInfo(root)
	if err != nil || !os.SameFile(store.runsIdentity, openedIdentity) {
		_ = root.Close()
		return nil, fmt.Errorf("%w: runs directory identity differs from NewStore", ErrEvidenceUnsafePath)
	}
	return root, nil
}

func (store *Store) finishRunsOperation(operationErr error) error {
	root, identityErr := store.openRuns()
	if identityErr == nil {
		if closeErr := root.Close(); closeErr != nil {
			return preferEvidenceIO(operationErr, "close final runs identity anchor", closeErr)
		}
	}
	if identityErr != nil {
		return errors.Join(identityErr, operationErr)
	}
	return operationErr
}

func rootDirectoryInfo(root *os.Root) (fs.FileInfo, error) {
	opened, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	info, statErr := opened.Stat()
	closeErr := opened.Close()
	if statErr != nil {
		return nil, statErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if !info.IsDir() {
		return nil, errors.New("anchored root is not a directory")
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
	components, volumeRoot, err := absolutePathComponents(path)
	if err != nil {
		return err
	}
	currentRoot, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return fmt.Errorf("%w: anchor filesystem root: %w", ErrEvidenceUnsafePath, err)
	}
	currentPath := volumeRoot
	for _, component := range components {
		currentPath = filepath.Join(currentPath, component)
		info, lstatErr := currentRoot.Lstat(component)
		if errors.Is(lstatErr, fs.ErrNotExist) {
			mkdirErr := currentRoot.Mkdir(component, 0o755)
			if mkdirErr != nil && !errors.Is(mkdirErr, fs.ErrExist) {
				_ = currentRoot.Close()
				return fmt.Errorf("%w: create directory %s: %w", ErrEvidenceUnsafePath, currentPath, mkdirErr)
			}
			info, lstatErr = currentRoot.Lstat(component)
		}
		if lstatErr != nil {
			_ = currentRoot.Close()
			return fmt.Errorf("%w: inspect directory %s: %w", ErrEvidenceUnsafePath, currentPath, lstatErr)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			_ = currentRoot.Close()
			return fmt.Errorf("%w: component %s is not a real directory", ErrEvidenceUnsafePath, currentPath)
		}
		childRoot, openErr := currentRoot.OpenRoot(component)
		if openErr != nil {
			_ = currentRoot.Close()
			return fmt.Errorf("%w: anchor directory %s: %w", ErrEvidenceUnsafePath, currentPath, openErr)
		}
		if verifyErr := verifyAnchoredChild(currentRoot, childRoot, component, info); verifyErr != nil {
			_ = childRoot.Close()
			_ = currentRoot.Close()
			return fmt.Errorf("%w: directory component %s changed while anchoring: %v", ErrEvidenceUnsafePath, currentPath, verifyErr)
		}
		if closeErr := currentRoot.Close(); closeErr != nil {
			_ = childRoot.Close()
			return fmt.Errorf("%w: close parent anchor for %s: %w", ErrEvidenceUnsafePath, currentPath, closeErr)
		}
		currentRoot = childRoot
	}
	if err := currentRoot.Close(); err != nil {
		return fmt.Errorf("%w: close prepared directory anchor: %w", ErrEvidenceUnsafePath, err)
	}
	return nil
}

func openRealDirectory(path string) (*os.Root, error) {
	components, volumeRoot, err := absolutePathComponents(path)
	if err != nil {
		return nil, err
	}
	currentRoot, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: anchor filesystem root: %w", ErrEvidenceUnsafePath, err)
	}
	currentPath := volumeRoot
	for _, component := range components {
		currentPath = filepath.Join(currentPath, component)
		info, lstatErr := currentRoot.Lstat(component)
		if lstatErr != nil {
			_ = currentRoot.Close()
			return nil, fmt.Errorf("%w: inspect directory %s: %w", ErrEvidenceUnsafePath, currentPath, lstatErr)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			_ = currentRoot.Close()
			return nil, fmt.Errorf("%w: component %s is not a real directory", ErrEvidenceUnsafePath, currentPath)
		}
		childRoot, openErr := currentRoot.OpenRoot(component)
		if openErr != nil {
			_ = currentRoot.Close()
			return nil, fmt.Errorf("%w: anchor directory %s: %w", ErrEvidenceUnsafePath, currentPath, openErr)
		}
		if verifyErr := verifyAnchoredChild(currentRoot, childRoot, component, info); verifyErr != nil {
			_ = childRoot.Close()
			_ = currentRoot.Close()
			return nil, fmt.Errorf("%w: directory component %s changed while anchoring: %v", ErrEvidenceUnsafePath, currentPath, verifyErr)
		}
		if closeErr := currentRoot.Close(); closeErr != nil {
			_ = childRoot.Close()
			return nil, fmt.Errorf("%w: close parent anchor for %s: %w", ErrEvidenceUnsafePath, currentPath, closeErr)
		}
		currentRoot = childRoot
	}
	return currentRoot, nil
}

func verifyAnchoredChild(parent, child *os.Root, name string, expected fs.FileInfo) error {
	opened, err := child.Open(".")
	if err != nil {
		return err
	}
	openedInfo, infoErr := opened.Stat()
	closeErr := opened.Close()
	current, currentErr := parent.Lstat(name)
	if infoErr != nil {
		return infoErr
	}
	if closeErr != nil {
		return closeErr
	}
	if currentErr != nil {
		return currentErr
	}
	if current.Mode()&fs.ModeSymlink != 0 || !current.IsDir() || !openedInfo.IsDir() || !os.SameFile(expected, current) || !os.SameFile(expected, openedInfo) {
		return errors.New("directory identity changed")
	}
	return nil
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
