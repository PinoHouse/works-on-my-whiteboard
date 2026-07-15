package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/immutablefile"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
)

const (
	manifestDirectoryName = "releases"
	manifestFileName      = "manifest.yaml"
)

type immutableManifestWriteFunc func(context.Context, string, fs.FileInfo, []byte) (immutablefile.Result, error)

type manifestStorageOperations struct {
	// writeNoReplace is deliberately the only file-creation seam. Temporary
	// creation, mode, full write, file sync, close, no-replace install,
	// directory sync, and cleanup are one Task 8 immutable-writer contract.
	// The release layer must not reproduce any part of that lifecycle.
	writeNoReplace immutableManifestWriteFunc
	syncDirectory  func(*os.Root, string) error
	syncFile       func(*os.File) error
	closeFile      func(*os.File) error
	lstat          func(*os.Root, string) (fs.FileInfo, error)
	readDirectory  func(*os.Root) ([]fs.DirEntry, error)
	readManifest   func(context.Context, io.Reader) ([]byte, error)
}

func defaultManifestStorageOperations() manifestStorageOperations {
	return manifestStorageOperations{
		writeNoReplace: immutablefile.WriteNoReplaceExpected,
		syncDirectory: func(directory *os.Root, _ string) error {
			opened, err := directory.Open(".")
			if err != nil {
				return err
			}
			return errors.Join(opened.Sync(), opened.Close())
		},
		syncFile: func(file *os.File) error {
			return file.Sync()
		},
		closeFile: func(file *os.File) error {
			return file.Close()
		},
		lstat: func(root *os.Root, name string) (fs.FileInfo, error) {
			return root.Lstat(name)
		},
		readDirectory: func(root *os.Root) ([]fs.DirEntry, error) {
			directory, err := root.Open(".")
			if err != nil {
				return nil, err
			}
			entries, readErr := directory.ReadDir(-1)
			return entries, errors.Join(readErr, directory.Close())
		},
		readManifest: readManifestBytes,
	}
}

// WriteManifest installs one canonical manifest without replacing any existing
// directory entry. An ErrSnapshotExists result is always a conflict, including
// when the existing bytes happen to be identical.
func WriteManifest(ctx context.Context, evidenceRoot string, manifest Manifest) error {
	return writeManifestWithOperations(ctx, evidenceRoot, manifest, defaultManifestStorageOperations())
}

func writeManifestWithOperations(ctx context.Context, evidenceRoot string, manifest Manifest, operations manifestStorageOperations) (resultErr error) {
	if err := validateManifestStorageContext(ctx); err != nil {
		return err
	}
	parsed, err := inputdigest.Parse(string(manifest.InputDigest))
	if err != nil {
		return err
	}
	data, err := Encode(manifest)
	if err != nil {
		return err
	}
	if len(data) > MaxManifestBytes {
		return fmt.Errorf("%w: %d bytes", ErrManifestTooLarge, len(data))
	}
	directory, destination, err := manifestStoragePaths(evidenceRoot, parsed)
	if err != nil {
		return err
	}
	chain, err := openManifestDirectoryChain(ctx, directory, true, operations)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := chain.close(); closeErr != nil {
			resultErr = errors.Join(resultErr, snapshotIOError("close manifest directory anchors", closeErr))
		}
	}()

	if err := chain.verify(ctx, operations); err != nil {
		return err
	}
	if err := chain.synchronizeAll(ctx, operations); err != nil {
		return err
	}
	if err := verifyExactManifestTargetIfPresent(chain.leaf(), operations); err != nil {
		return err
	}
	writeResult, writeErr := operations.writeNoReplace(ctx, destination, chain.leaf().info, data)
	if writeErr == nil && !writeResult.Installed {
		writeErr = errors.New("immutable writer reported success without installation")
	}
	verificationErr := chain.verify(ctx, operations)
	targetVerificationErr := verifyExactManifestTargetIfPresent(chain.leaf(), operations)
	if writeErr != nil || verificationErr != nil || targetVerificationErr != nil {
		return errors.Join(classifyManifestWriterError(writeResult, writeErr), classifyManifestWriteError(verificationErr), targetVerificationErr)
	}
	return nil
}

// LoadManifest strictly loads the immutable manifest at the digest-derived
// path. It does not search other digests or mutate durability state.
func LoadManifest(ctx context.Context, evidenceRoot string, digest inputdigest.Digest) (Manifest, error) {
	return loadManifestWithOperations(ctx, evidenceRoot, digest, false, defaultManifestStorageOperations())
}

// LoadAndSyncManifest loads one immutable manifest, synchronizes that same
// opened file and its anchored directory chain, verifies the snapshot again,
// and returns the manifest whose durability was established.
func LoadAndSyncManifest(ctx context.Context, evidenceRoot string, digest inputdigest.Digest) (Manifest, error) {
	return loadManifestWithOperations(ctx, evidenceRoot, digest, true, defaultManifestStorageOperations())
}

// SyncManifest strictly validates the existing manifest, synchronizes the
// regular file and the digest-to-evidence-root directory chain, and leaves the
// manifest bytes and metadata untouched.
func SyncManifest(ctx context.Context, evidenceRoot string, digest inputdigest.Digest) error {
	_, err := LoadAndSyncManifest(ctx, evidenceRoot, digest)
	return err
}

func loadManifestWithOperations(ctx context.Context, evidenceRoot string, digest inputdigest.Digest, synchronize bool, operations manifestStorageOperations) (manifest Manifest, resultErr error) {
	if err := validateManifestStorageContext(ctx); err != nil {
		return Manifest{}, err
	}
	parsed, err := inputdigest.Parse(string(digest))
	if err != nil {
		return Manifest{}, err
	}
	directory, _, err := manifestStoragePaths(evidenceRoot, parsed)
	if err != nil {
		return Manifest{}, err
	}
	chain, err := openManifestDirectoryChain(ctx, directory, false, operations)
	if err != nil {
		return Manifest{}, err
	}
	defer func() {
		if closeErr := chain.close(); closeErr != nil {
			manifest = Manifest{}
			resultErr = errors.Join(resultErr, snapshotIOError("close manifest directory anchors", closeErr))
		}
	}()

	manifest, err = readAnchoredManifest(ctx, chain, parsed, synchronize, operations)
	if err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func readAnchoredManifest(ctx context.Context, chain *manifestDirectoryChain, digest inputdigest.Digest, synchronize bool, operations manifestStorageOperations) (result Manifest, resultErr error) {
	if err := chain.verify(ctx, operations); err != nil {
		return Manifest{}, err
	}
	leaf := chain.leaf()
	expected, err := operations.lstat(leaf.root, manifestFileName)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, fmt.Errorf("%w: manifest target is absent: %w", ErrSnapshotNotFound, err)
	}
	if err != nil {
		return Manifest{}, snapshotIOError("inspect manifest target", err)
	}
	if err := requireExactManifestEntryName(leaf, manifestFileName, operations); err != nil {
		return Manifest{}, err
	}
	if expected.Mode()&fs.ModeSymlink != 0 || !expected.Mode().IsRegular() {
		return Manifest{}, fmt.Errorf("%w: manifest target is not a real regular file", ErrSnapshotUnsafePath)
	}
	if expected.Size() < 0 {
		return Manifest{}, fmt.Errorf("%w: manifest target has a negative size", ErrSnapshotUnsafePath)
	}
	if expected.Size() > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: %d bytes", ErrManifestTooLarge, expected.Size())
	}

	file, err := leaf.root.OpenFile(manifestFileName, os.O_RDONLY, 0)
	if err != nil {
		return Manifest{}, reconcileManifestOpenError(leaf, expected, err, operations)
	}
	defer func() {
		if closeErr := operations.closeFile(file); closeErr != nil {
			resultErr = errors.Join(resultErr, snapshotIOError("close manifest file", closeErr))
		}
	}()

	opened, err := file.Stat()
	if err != nil {
		return Manifest{}, snapshotIOError("stat opened manifest", err)
	}
	current, err := operations.lstat(leaf.root, manifestFileName)
	if err != nil {
		return Manifest{}, classifySecondaryLstatError("re-inspect opened manifest", err)
	}
	if !sameRegularFileSnapshot(expected, opened) || !sameRegularFileSnapshot(expected, current) {
		return Manifest{}, fmt.Errorf("%w: manifest identity changed while opening", ErrSnapshotUnsafePath)
	}

	data, err := operations.readManifest(ctx, file)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrManifestTooLarge) || errors.Is(err, ErrSnapshotIO) {
			return Manifest{}, err
		}
		return Manifest{}, snapshotIOError("read manifest file", err)
	}
	if int64(len(data)) != expected.Size() {
		return Manifest{}, fmt.Errorf("%w: manifest read length %d differs from inspected size %d", ErrSnapshotUnsafePath, len(data), expected.Size())
	}
	decoded, err := Decode(data)
	if err != nil {
		if errors.Is(err, ErrManifestTooLarge) {
			return Manifest{}, err
		}
		return Manifest{}, fmt.Errorf("%w: %w", ErrSnapshotCorrupt, err)
	}
	if decoded.InputDigest != digest {
		return Manifest{}, fmt.Errorf("%w: manifest digest %q differs from path digest %q", ErrSnapshotCorrupt, decoded.InputDigest, digest)
	}

	if synchronize {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		if err := operations.syncFile(file); err != nil {
			return Manifest{}, snapshotIOError("synchronize manifest file", err)
		}
		for index := len(chain.entries) - 1; index >= chain.evidenceRootIndex; index-- {
			if err := ctx.Err(); err != nil {
				return Manifest{}, err
			}
			entry := chain.entries[index]
			if err := operations.syncDirectory(entry.root, entry.path); err != nil {
				return Manifest{}, snapshotIOError("synchronize manifest directory "+entry.path, err)
			}
		}
	}

	if err := verifyManifestSnapshot(ctx, chain, expected, file, operations); err != nil {
		return Manifest{}, err
	}
	return cloneManifest(decoded), nil
}

func readManifestBytes(ctx context.Context, reader io.Reader) ([]byte, error) {
	const readLimit = MaxManifestBytes + 1
	data := make([]byte, 0, 4096)
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := readLimit - len(data)
		if remaining <= 0 {
			return nil, fmt.Errorf("%w: more than %d bytes", ErrManifestTooLarge, MaxManifestBytes)
		}
		request := buffer
		if len(request) > remaining {
			request = request[:remaining]
		}
		count, readErr := reader.Read(request)
		if count < 0 || count > len(request) {
			return nil, snapshotIOError("read manifest file", fmt.Errorf("invalid read count %d for buffer length %d", count, len(request)))
		}
		if count > 0 {
			data = appendManifestBytesWithinLimit(data, request[:count], readLimit)
			if len(data) > MaxManifestBytes {
				return nil, fmt.Errorf("%w: more than %d bytes", ErrManifestTooLarge, MaxManifestBytes)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return data, nil
		}
		if readErr != nil {
			return nil, snapshotIOError("read manifest file", readErr)
		}
		if count == 0 {
			return nil, snapshotIOError("read manifest file", io.ErrNoProgress)
		}
	}
}

func appendManifestBytesWithinLimit(data, addition []byte, limit int) []byte {
	required := len(data) + len(addition)
	if required <= cap(data) {
		originalLength := len(data)
		data = data[:required]
		copy(data[originalLength:], addition)
		return data
	}
	capacity := cap(data) * 2
	if capacity < required {
		capacity = required
	}
	if capacity > limit {
		capacity = limit
	}
	grown := make([]byte, len(data), capacity)
	copy(grown, data)
	originalLength := len(grown)
	grown = grown[:required]
	copy(grown[originalLength:], addition)
	return grown
}

func verifyManifestSnapshot(ctx context.Context, chain *manifestDirectoryChain, expected fs.FileInfo, file *os.File, operations manifestStorageOperations) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil {
		return snapshotIOError("re-stat opened manifest", err)
	}
	if err := requireExactManifestEntryName(chain.leaf(), manifestFileName, operations); err != nil {
		return err
	}
	current, err := operations.lstat(chain.leaf().root, manifestFileName)
	if err != nil {
		return classifySecondaryLstatError("re-inspect manifest after read", err)
	}
	if !sameRegularFileSnapshot(expected, opened) || !sameRegularFileSnapshot(expected, current) {
		return fmt.Errorf("%w: manifest changed while it was read", ErrSnapshotUnsafePath)
	}
	return chain.verify(ctx, operations)
}

type manifestDirectoryEntry struct {
	root *os.Root
	path string
	name string
	info fs.FileInfo
}

type manifestDirectoryChain struct {
	entries           []manifestDirectoryEntry
	evidenceRootIndex int
}

func (chain *manifestDirectoryChain) leaf() manifestDirectoryEntry {
	return chain.entries[len(chain.entries)-1]
}

func (chain *manifestDirectoryChain) close() error {
	causes := make([]error, 0, len(chain.entries))
	for index := len(chain.entries) - 1; index >= 0; index-- {
		causes = append(causes, chain.entries[index].root.Close())
	}
	return errors.Join(causes...)
}

func (chain *manifestDirectoryChain) verify(ctx context.Context, operations manifestStorageOperations) error {
	for index, entry := range chain.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		opened, err := entry.root.Open(".")
		if err != nil {
			return snapshotIOError("open anchored directory "+entry.path, err)
		}
		openedInfo, statErr := opened.Stat()
		closeErr := opened.Close()
		if statErr != nil || closeErr != nil {
			return snapshotIOError("inspect anchored directory "+entry.path, errors.Join(statErr, closeErr))
		}
		if !openedInfo.IsDir() || !os.SameFile(entry.info, openedInfo) {
			return fmt.Errorf("%w: anchored directory %q changed identity", ErrSnapshotUnsafePath, entry.path)
		}
		if index == 0 {
			continue
		}
		parent := chain.entries[index-1]
		if err := requireExactManifestEntryName(parent, entry.name, operations); err != nil {
			return err
		}
		current, err := operations.lstat(parent.root, entry.name)
		if err != nil {
			return classifySecondaryLstatError("re-inspect directory relationship "+entry.path, err)
		}
		if current.Mode()&fs.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(entry.info, current) {
			return fmt.Errorf("%w: directory relationship %q changed", ErrSnapshotUnsafePath, entry.path)
		}
	}
	return nil
}

func (chain *manifestDirectoryChain) synchronizeAll(ctx context.Context, operations manifestStorageOperations) error {
	for index := len(chain.entries) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := chain.entries[index]
		if err := operations.syncDirectory(entry.root, entry.path); err != nil {
			return snapshotIOError("synchronize manifest directory chain "+entry.path, err)
		}
	}
	return chain.verify(ctx, operations)
}

func openManifestDirectoryChain(ctx context.Context, directory string, create bool, operations manifestStorageOperations) (*manifestDirectoryChain, error) {
	if err := validateManifestStorageContext(ctx); err != nil {
		return nil, err
	}
	if directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, fmt.Errorf("%w: manifest directory must be a clean absolute path", ErrSnapshotUnsafePath)
	}
	volume := filepath.VolumeName(directory)
	volumeRoot := volume + string(filepath.Separator)
	remainder := strings.TrimPrefix(directory, volumeRoot)
	components := strings.Split(remainder, string(filepath.Separator))
	if remainder == "" {
		components = nil
	}

	root, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return nil, snapshotIOError("open manifest volume root", err)
	}
	rootInfo, err := inspectManifestDirectory(root, volumeRoot)
	if err != nil {
		return nil, errors.Join(err, snapshotIOError("close manifest volume root", root.Close()))
	}
	chain := &manifestDirectoryChain{
		entries: []manifestDirectoryEntry{{root: root, path: volumeRoot, info: rootInfo}},
	}
	closeWith := func(cause error) (*manifestDirectoryChain, error) {
		return nil, errors.Join(cause, snapshotIOError("close incomplete manifest directory chain", chain.close()))
	}

	currentPath := volumeRoot
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return closeWith(fmt.Errorf("%w: invalid manifest directory component", ErrSnapshotUnsafePath))
		}
		if err := ctx.Err(); err != nil {
			return closeWith(err)
		}
		parent := chain.entries[len(chain.entries)-1]
		expected, lstatErr := operations.lstat(parent.root, component)
		created := false
		secondaryLstat := false
		if errors.Is(lstatErr, fs.ErrNotExist) {
			if !create {
				return closeWith(fmt.Errorf("%w: manifest directory component %q is absent: %w", ErrSnapshotNotFound, component, lstatErr))
			}
			mkdirErr := parent.root.Mkdir(component, 0o755)
			if mkdirErr == nil {
				created = true
			} else if !errors.Is(mkdirErr, fs.ErrExist) {
				return closeWith(snapshotIOError("create manifest directory component "+component, mkdirErr))
			}
			secondaryLstat = true
			expected, lstatErr = operations.lstat(parent.root, component)
		}
		if lstatErr != nil {
			if secondaryLstat && errors.Is(lstatErr, fs.ErrNotExist) {
				return closeWith(classifySecondaryLstatError("re-inspect created manifest directory component "+component, lstatErr))
			}
			return closeWith(snapshotIOError("inspect manifest directory component "+component, lstatErr))
		}
		if err := requireExactManifestEntryName(parent, component, operations); err != nil {
			return closeWith(err)
		}
		if expected.Mode()&fs.ModeSymlink != 0 || !expected.IsDir() {
			return closeWith(fmt.Errorf("%w: manifest directory component %q is not a real directory", ErrSnapshotUnsafePath, component))
		}

		child, openErr := parent.root.OpenRoot(component)
		if openErr != nil {
			return closeWith(reconcileDirectoryOpenError(parent, component, expected, openErr, operations))
		}
		currentPath = filepath.Join(currentPath, component)
		childInfo, inspectErr := inspectManifestDirectory(child, currentPath)
		if inspectErr != nil {
			return closeWith(errors.Join(inspectErr, snapshotIOError("close unverified manifest directory "+currentPath, child.Close())))
		}
		current, currentErr := operations.lstat(parent.root, component)
		if currentErr != nil {
			return closeWith(errors.Join(classifySecondaryLstatError("re-inspect anchored manifest directory component "+component, currentErr), snapshotIOError("close unverified manifest directory "+currentPath, child.Close())))
		}
		if current.Mode()&fs.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(expected, current) || !os.SameFile(expected, childInfo) {
			cause := fmt.Errorf("%w: manifest directory component %q changed while anchoring", ErrSnapshotUnsafePath, component)
			return closeWith(errors.Join(cause, snapshotIOError("close changed manifest directory "+currentPath, child.Close())))
		}
		chain.entries = append(chain.entries, manifestDirectoryEntry{root: child, path: currentPath, name: component, info: childInfo})
		if created {
			if err := operations.syncDirectory(child, currentPath); err != nil {
				return closeWith(snapshotIOError("synchronize new manifest directory "+currentPath, err))
			}
			if err := operations.syncDirectory(parent.root, parent.path); err != nil {
				return closeWith(snapshotIOError("synchronize new manifest parent "+parent.path, err))
			}
		}
	}
	chain.evidenceRootIndex = len(chain.entries) - 3
	if chain.evidenceRootIndex <= 0 {
		return closeWith(fmt.Errorf("%w: evidence root is not below the volume root", ErrSnapshotUnsafePath))
	}
	return chain, nil
}

func inspectManifestDirectory(root *os.Root, path string) (fs.FileInfo, error) {
	opened, err := root.Open(".")
	if err != nil {
		return nil, snapshotIOError("open manifest directory "+path, err)
	}
	info, statErr := opened.Stat()
	closeErr := opened.Close()
	if statErr != nil || closeErr != nil {
		return nil, snapshotIOError("inspect manifest directory "+path, errors.Join(statErr, closeErr))
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: anchored manifest component %q is not a directory", ErrSnapshotUnsafePath, path)
	}
	return info, nil
}

func requireExactManifestEntryName(parent manifestDirectoryEntry, expected string, operations manifestStorageOperations) error {
	entries, err := operations.readDirectory(parent.root)
	if err != nil {
		return snapshotIOError("enumerate manifest directory "+parent.path, err)
	}
	for _, entry := range entries {
		if entry.Name() == expected {
			return nil
		}
	}
	return fmt.Errorf("%w: directory %q has no exact entry named %q", ErrSnapshotUnsafePath, parent.path, expected)
}

func verifyExactManifestTargetIfPresent(leaf manifestDirectoryEntry, operations manifestStorageOperations) error {
	_, err := operations.lstat(leaf.root, manifestFileName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return snapshotIOError("inspect manifest target name", err)
	}
	return requireExactManifestEntryName(leaf, manifestFileName, operations)
}

func manifestStoragePaths(evidenceRoot string, digest inputdigest.Digest) (string, string, error) {
	if evidenceRoot == "" || !filepath.IsAbs(evidenceRoot) || filepath.Clean(evidenceRoot) != evidenceRoot {
		return "", "", fmt.Errorf("%w: evidence root must be a clean absolute path", ErrSnapshotUnsafePath)
	}
	volumeRoot := filepath.VolumeName(evidenceRoot) + string(filepath.Separator)
	if evidenceRoot == volumeRoot {
		return "", "", fmt.Errorf("%w: evidence root may not be a volume root", ErrSnapshotUnsafePath)
	}
	hex := strings.TrimPrefix(string(digest), "sha256:")
	directory := filepath.Join(evidenceRoot, manifestDirectoryName, "sha256-"+hex)
	return directory, filepath.Join(directory, manifestFileName), nil
}

func validateManifestStorageContext(ctx context.Context) error {
	if ctx == nil {
		return snapshotIOError("validate manifest operation", errors.New("context is nil"))
	}
	return ctx.Err()
}

func reconcileDirectoryOpenError(parent manifestDirectoryEntry, component string, expected fs.FileInfo, openErr error, operations manifestStorageOperations) error {
	current, currentErr := operations.lstat(parent.root, component)
	if currentErr != nil {
		causes := errors.Join(openErr, currentErr)
		if errors.Is(currentErr, fs.ErrNotExist) {
			return fmt.Errorf("%w: directory component %q disappeared before anchoring: %w", ErrSnapshotUnsafePath, component, causes)
		}
		return snapshotIOError("re-inspect directory component "+component+" after anchor failure", causes)
	}
	if current.Mode()&fs.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(expected, current) {
		return fmt.Errorf("%w: directory component %q changed before anchoring: %w", ErrSnapshotUnsafePath, component, openErr)
	}
	if errors.Is(openErr, fs.ErrNotExist) {
		return fmt.Errorf("%w: directory component %q transiently disappeared before anchoring: %w", ErrSnapshotUnsafePath, component, openErr)
	}
	return snapshotIOError("anchor manifest directory component "+component, openErr)
}

func reconcileManifestOpenError(leaf manifestDirectoryEntry, expected fs.FileInfo, openErr error, operations manifestStorageOperations) error {
	current, currentErr := operations.lstat(leaf.root, manifestFileName)
	if currentErr != nil {
		causes := errors.Join(openErr, currentErr)
		if errors.Is(currentErr, fs.ErrNotExist) {
			return fmt.Errorf("%w: manifest target disappeared before opening: %w", ErrSnapshotUnsafePath, causes)
		}
		return snapshotIOError("re-inspect manifest target after open failure", causes)
	}
	if current.Mode()&fs.ModeSymlink != 0 || !current.Mode().IsRegular() || !sameRegularFileSnapshot(expected, current) {
		return fmt.Errorf("%w: manifest target changed before opening: %w", ErrSnapshotUnsafePath, openErr)
	}
	if errors.Is(openErr, fs.ErrNotExist) {
		return fmt.Errorf("%w: manifest target transiently disappeared before opening: %w", ErrSnapshotUnsafePath, openErr)
	}
	return snapshotIOError("open manifest target", openErr)
}

func classifySecondaryLstatError(operation string, cause error) error {
	if errors.Is(cause, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s: %w", ErrSnapshotUnsafePath, operation, cause)
	}
	return snapshotIOError(operation, cause)
}

func sameRegularFileSnapshot(left, right fs.FileInfo) bool {
	return left != nil && right != nil && left.Mode().IsRegular() && right.Mode().IsRegular() &&
		os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func classifyManifestWriteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrSnapshotUnsafePath) || errors.Is(err, ErrSnapshotIO) {
		return err
	}
	if errors.Is(err, immutablefile.ErrUnsafePath) || errors.Is(err, immutablefile.ErrInvalid) {
		return fmt.Errorf("%w: %w", ErrSnapshotUnsafePath, err)
	}
	if errors.Is(err, immutablefile.ErrExists) {
		return fmt.Errorf("%w: %w", ErrSnapshotExists, err)
	}
	return snapshotIOError("write immutable manifest", err)
}

type immutableWriterErrorAnalysis struct {
	hasExists                 bool
	hasImmutableExists        bool
	hasFileSystemExists       bool
	hasSystemExists           bool
	hasUnsafe                 bool
	hasImmutableUnsafe        bool
	hasImmutableInvalid       bool
	hasContextCanceled        bool
	hasContextDeadline        bool
	hasReleaseUnsafe          bool
	hasOperational            bool
	writerErrors              int
	pureExistsMetadata        bool
	malformedOrOverBudgetTree bool
}

const (
	// Error values cross the immutable-writer boundary and are not assumed to
	// form a finite tree. These limits bound both stack storage and work.
	maximumImmutableWriterErrorDepth = 64
	maximumImmutableWriterErrorNodes = 256
)

type immutableWriterErrorNode struct {
	err   error
	depth int
}

type pureSnapshotExistsError struct {
	cause error
}

func (err pureSnapshotExistsError) Error() string {
	return fmt.Sprintf("%s: %v", ErrSnapshotExists, err.cause)
}

func (err pureSnapshotExistsError) Unwrap() []error {
	return []error{ErrSnapshotExists, err.cause}
}

// IsPureSnapshotExists reports whether err contains only a release-storage
// certified immutable destination conflict and optional unary wrappers. A bare
// ErrSnapshotExists value, an Is-only lookalike, or any joined sibling is not
// proof that retrying through the installed winner is safe.
func IsPureSnapshotExists(err error) bool {
	const maximumWrapperDepth = 64
	for depth := 0; err != nil && depth < maximumWrapperDepth; depth++ {
		if _, ok := err.(pureSnapshotExistsError); ok {
			return true
		}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			causes := joined.Unwrap()
			if len(causes) != 1 || causes[0] == nil {
				return false
			}
			err = causes[0]
			continue
		}
		if wrapped, ok := err.(interface{ Unwrap() error }); ok {
			err = wrapped.Unwrap()
			continue
		}
		return false
	}
	return false
}

func classifyManifestWriterError(result immutablefile.Result, err error) error {
	if err == nil {
		return nil
	}
	analysis := immutableWriterErrorAnalysis{pureExistsMetadata: true}
	inspectImmutableWriterErrorTree(err, result, &analysis)
	if analysis.malformedOrOverBudgetTree {
		return classifyMalformedManifestWriterError(analysis)
	}
	if analysis.hasUnsafe {
		unsafeErr := fmt.Errorf("%w: %w", ErrSnapshotUnsafePath, err)
		if analysis.hasOperational {
			return errors.Join(unsafeErr, snapshotIOError("write immutable manifest", err))
		}
		return unsafeErr
	}
	if analysis.hasExists {
		if analysis.writerErrors == 1 && analysis.pureExistsMetadata && !analysis.hasOperational {
			return pureSnapshotExistsError{cause: err}
		}
		return errors.Join(
			fmt.Errorf("%w: immutable destination conflict had additional failure", ErrSnapshotExists),
			snapshotIOError("write immutable manifest", err),
		)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrSnapshotUnsafePath) || errors.Is(err, ErrSnapshotIO) {
		return err
	}
	return snapshotIOError("write immutable manifest", err)
}

func inspectImmutableWriterErrorTree(err error, result immutablefile.Result, analysis *immutableWriterErrorAnalysis) {
	stack := []immutableWriterErrorNode{{err: err}}
	visited := 0
	for len(stack) > 0 {
		index := len(stack) - 1
		current := stack[index]
		stack = stack[:index]
		if current.depth > maximumImmutableWriterErrorDepth || visited >= maximumImmutableWriterErrorNodes {
			analysis.malformedOrOverBudgetTree = true
			analysis.hasOperational = true
			continue
		}
		visited++
		if current.err == nil {
			analysis.hasOperational = true
			continue
		}
		if writerErr, ok := current.err.(*immutablefile.Error); ok {
			if writerErr == nil {
				analysis.hasOperational = true
				continue
			}
			analysis.writerErrors++
			if !validImmutableWriterStage(writerErr.Stage) || writerErr.Installed != result.Installed {
				analysis.hasOperational = true
			}
			if writerErr.Installed || writerErr.Stage != immutablefile.StageValidatePath && writerErr.Stage != immutablefile.StageInstall {
				analysis.pureExistsMetadata = false
			}
			stack = appendImmutableWriterErrorChildren(stack, []error{writerErr.Err}, current.depth, visited, analysis)
			continue
		}
		if joined, ok := current.err.(interface{ Unwrap() []error }); ok {
			causes := joined.Unwrap()
			if len(causes) == 0 {
				analysis.hasOperational = true
				continue
			}
			stack = appendImmutableWriterErrorChildren(stack, causes, current.depth, visited, analysis)
			continue
		}
		if wrapped, ok := current.err.(interface{ Unwrap() error }); ok {
			cause := wrapped.Unwrap()
			if cause == nil {
				analysis.hasOperational = true
				continue
			}
			stack = appendImmutableWriterErrorChildren(stack, []error{cause}, current.depth, visited, analysis)
			continue
		}
		inspectImmutableWriterErrorLeaf(current.err, analysis)
	}
}

func appendImmutableWriterErrorChildren(stack []immutableWriterErrorNode, causes []error, parentDepth, visited int, analysis *immutableWriterErrorAnalysis) []immutableWriterErrorNode {
	remaining := maximumImmutableWriterErrorNodes - visited - len(stack)
	if remaining < 0 {
		remaining = 0
	}
	childCount := len(causes)
	if childCount > remaining {
		childCount = remaining
		analysis.malformedOrOverBudgetTree = true
		analysis.hasOperational = true
	}
	for index := childCount - 1; index >= 0; index-- {
		stack = append(stack, immutableWriterErrorNode{err: causes[index], depth: parentDepth + 1})
	}
	return stack
}

func inspectImmutableWriterErrorLeaf(err error, analysis *immutableWriterErrorAnalysis) {
	switch err {
	case immutablefile.ErrExists:
		analysis.hasExists = true
		analysis.hasImmutableExists = true
	case fs.ErrExist:
		analysis.hasExists = true
		analysis.hasFileSystemExists = true
	case syscall.EEXIST:
		analysis.hasExists = true
		analysis.hasSystemExists = true
	case immutablefile.ErrUnsafePath:
		analysis.hasUnsafe = true
		analysis.hasImmutableUnsafe = true
	case immutablefile.ErrInvalid:
		analysis.hasUnsafe = true
		analysis.hasImmutableInvalid = true
	case context.Canceled:
		analysis.hasContextCanceled = true
		analysis.hasOperational = true
	case context.DeadlineExceeded:
		analysis.hasContextDeadline = true
		analysis.hasOperational = true
	case ErrSnapshotUnsafePath:
		analysis.hasReleaseUnsafe = true
		analysis.hasOperational = true
	case ErrSnapshotIO:
		analysis.hasOperational = true
	default:
		if errors.Is(err, immutablefile.ErrExists) || errors.Is(err, fs.ErrExist) {
			analysis.hasExists = true
		}
		if errors.Is(err, immutablefile.ErrUnsafePath) || errors.Is(err, immutablefile.ErrInvalid) {
			analysis.hasUnsafe = true
		}
		if errors.Is(err, context.Canceled) {
			analysis.hasContextCanceled = true
		}
		if errors.Is(err, context.DeadlineExceeded) {
			analysis.hasContextDeadline = true
		}
		if errors.Is(err, ErrSnapshotUnsafePath) {
			analysis.hasReleaseUnsafe = true
		}
		analysis.hasOperational = true
	}
}

func classifyMalformedManifestWriterError(analysis immutableWriterErrorAnalysis) error {
	// Never retain the original tree here. A later errors.Is/errors.As walk of
	// a cyclic cause would otherwise reintroduce the hang this guard prevents.
	detail := errors.New("immutable writer error tree is cyclic or exceeds the inspection budget")
	classified := make([]error, 0, 8)
	if analysis.hasUnsafe || analysis.hasReleaseUnsafe {
		classified = append(classified, fmt.Errorf("%w: %v", ErrSnapshotUnsafePath, detail))
		if analysis.hasImmutableUnsafe {
			classified = append(classified, immutablefile.ErrUnsafePath)
		}
		if analysis.hasImmutableInvalid {
			classified = append(classified, immutablefile.ErrInvalid)
		}
	} else if analysis.hasExists {
		classified = append(classified, fmt.Errorf("%w: %v", ErrSnapshotExists, detail))
		if analysis.hasImmutableExists {
			classified = append(classified, immutablefile.ErrExists)
		}
		if analysis.hasFileSystemExists {
			classified = append(classified, fs.ErrExist)
		}
		if analysis.hasSystemExists {
			classified = append(classified, syscall.EEXIST)
		}
	}
	if analysis.hasContextCanceled {
		classified = append(classified, context.Canceled)
	}
	if analysis.hasContextDeadline {
		classified = append(classified, context.DeadlineExceeded)
	}
	classified = append(classified, snapshotIOError("write immutable manifest", detail))
	return errors.Join(classified...)
}

func validImmutableWriterStage(stage immutablefile.Stage) bool {
	switch stage {
	case immutablefile.StageValidatePath,
		immutablefile.StageCreateTemp,
		immutablefile.StageSetMode,
		immutablefile.StageWrite,
		immutablefile.StageShortWrite,
		immutablefile.StageSyncFile,
		immutablefile.StageCloseFile,
		immutablefile.StageInstall,
		immutablefile.StageSyncInstall,
		immutablefile.StageRemoveTemp,
		immutablefile.StageSyncCleanup,
		immutablefile.StageCloseDirectory:
		return true
	default:
		return false
	}
}

func snapshotIOError(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", ErrSnapshotIO, operation, cause)
}
