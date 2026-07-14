package inputdigest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	objectIDPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	runFilePattern  = regexp.MustCompile(`^run-[0-9]{8}T[0-9]{6}\.[0-9]{3}Z-[0-9a-f]{32}\.json$`)
	releasePattern  = regexp.MustCompile(`^sha256-[0-9a-f]{64}/manifest\.yaml$`)
)

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type systemGitRunner struct{}

type inspectionIndexContextKey struct{}

func (systemGitRunner) Run(ctx context.Context, root string, arguments ...string) ([]byte, error) {
	commandArguments := []string{
		"-C", root,
		"-c", "core.trustctime=true",
		"-c", "core.checkStat=default",
		"-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false",
		"-c", "core.ignoreStat=false",
	}
	commandArguments = append(commandArguments, arguments...)
	command := exec.CommandContext(ctx, "git", commandArguments...)
	command.Env = sanitizedGitEnvironment(os.Environ())
	if indexPath, ok := ctx.Value(inspectionIndexContextKey{}).(string); ok && indexPath != "" {
		command.Env = append(command.Env, "GIT_INDEX_FILE="+indexPath)
	}
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return nil, fmt.Errorf("git command failed: %w: %s", err, strings.TrimSpace(string(exitError.Stderr)))
		}
		return nil, fmt.Errorf("git command failed: %w", err)
	}
	return output, nil
}

func sanitizedGitEnvironment(source []string) []string {
	environment := make([]string, 0, len(source)+4)
	for _, item := range source {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(name, "GIT_") || name == "LC_ALL" || name == "LANG" {
			continue
		}
		environment = append(environment, item)
	}
	return append(environment, "LC_ALL=C", "LANG=C", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1")
}

func Compute(ctx context.Context, root string) (Digest, error) {
	state, err := ComputeState(ctx, root)
	if err != nil {
		return "", err
	}
	return state.InputDigest, nil
}

func ComputeState(ctx context.Context, root string) (State, error) {
	if ctx == nil {
		return State{}, fmt.Errorf("%w: context is nil", ErrRepository)
	}
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	return computeState(ctx, root, systemGitRunner{})
}

func computeState(ctx context.Context, root string, runner commandRunner) (State, error) {
	resolvedRoot, err := resolveRepositoryRoot(ctx, root, runner)
	if err != nil {
		return State{}, err
	}

	before, err := captureRepositorySnapshot(ctx, resolvedRoot, runner)
	if err != nil {
		return State{}, err
	}
	indexEntries, err := parseIndexEntries(before.index)
	if err != nil {
		return State{}, err
	}
	if err := validateTrackedArtifacts(resolvedRoot, indexEntries); err != nil {
		return State{}, err
	}
	if err := validateIndexFlags(before.flags, indexEntries); err != nil {
		return State{}, err
	}
	if err := validateSnapshot(resolvedRoot, before); err != nil {
		return State{}, err
	}

	entries := make([]Entry, 0, len(indexEntries))
	for _, indexEntry := range indexEntries {
		if isExcludedTrackedArtifact(indexEntry.path) {
			continue
		}
		blob, runErr := runner.Run(ctx, resolvedRoot, "cat-file", "blob", indexEntry.objectID)
		if runErr != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return State{}, contextErr
			}
			return State{}, fmt.Errorf("%w: cannot read indexed blob for %q: %v", ErrRepository, indexEntry.path, runErr)
		}
		entries = append(entries, Entry{Path: indexEntry.path, Mode: indexEntry.mode, Data: blob})
	}

	after, err := captureRepositorySnapshot(ctx, resolvedRoot, runner)
	if err != nil {
		return State{}, err
	}
	if !before.equal(after) {
		return State{}, fmt.Errorf("%w", ErrStateChanged)
	}
	if err := validateSnapshot(resolvedRoot, after); err != nil {
		return State{}, err
	}
	if err := validateTrackedArtifacts(resolvedRoot, indexEntries); err != nil {
		return State{}, err
	}
	digest, err := ComputeEntries(entries)
	if err != nil {
		return State{}, err
	}
	return State{InputDigest: digest, SourceCommit: before.head}, nil
}

func resolveRepositoryRoot(ctx context.Context, root string, runner commandRunner) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("%w: repository root is blank", ErrRepository)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve repository root", ErrRepository)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("%w: resolve repository root", ErrRepository)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: repository root is not a directory", ErrRepository)
	}

	bare, err := runner.Run(ctx, resolved, "rev-parse", "--is-bare-repository")
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return "", contextErr
		}
		return "", fmt.Errorf("%w: root is not a Git repository", ErrRepository)
	}
	if strings.TrimSpace(string(bare)) != "false" {
		return "", fmt.Errorf("%w: bare repository is unsupported", ErrRepository)
	}
	topOutput, err := runner.Run(ctx, resolved, "rev-parse", "--show-toplevel")
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return "", contextErr
		}
		return "", fmt.Errorf("%w: cannot resolve Git top-level", ErrRepository)
	}
	top, err := filepath.EvalSymlinks(strings.TrimSpace(string(topOutput)))
	if err != nil || top != resolved {
		return "", fmt.Errorf("%w: root must be the exact Git top-level", ErrRepository)
	}
	return resolved, nil
}

type repositorySnapshot struct {
	head      string
	rawIndex  rawIndexSnapshot
	index     []byte
	flags     []byte
	staged    []byte
	unstaged  []byte
	untracked []byte
	ignored   []byte
	worktree  []byte
}

func (snapshot repositorySnapshot) equal(other repositorySnapshot) bool {
	return snapshot.head == other.head &&
		snapshot.rawIndex.equal(other.rawIndex) &&
		bytes.Equal(snapshot.index, other.index) &&
		bytes.Equal(snapshot.flags, other.flags) &&
		bytes.Equal(snapshot.staged, other.staged) &&
		bytes.Equal(snapshot.unstaged, other.unstaged) &&
		bytes.Equal(snapshot.untracked, other.untracked) &&
		bytes.Equal(snapshot.ignored, other.ignored) &&
		bytes.Equal(snapshot.worktree, other.worktree)
}

func captureRepositorySnapshot(ctx context.Context, root string, runner commandRunner) (repositorySnapshot, error) {
	return captureRepositorySnapshotOnce(ctx, root, runner)
}

func captureRepositorySnapshotOnce(ctx context.Context, root string, runner commandRunner) (repositorySnapshot, error) {
	headOutput, err := runner.Run(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return repositorySnapshot{}, contextErr
		}
		return repositorySnapshot{}, fmt.Errorf("%w: HEAD is unborn or unavailable", ErrRepository)
	}
	head := strings.TrimSpace(string(headOutput))
	if !objectIDPattern.MatchString(head) {
		return repositorySnapshot{}, fmt.Errorf("%w: HEAD object ID is invalid", ErrRepository)
	}
	rawBefore, err := readRawIndex(ctx, root, runner)
	if err != nil {
		return repositorySnapshot{}, err
	}
	inspectionIndex, err := createInspectionIndex(rawBefore.Data)
	if err != nil {
		return repositorySnapshot{}, err
	}
	defer func() { _ = os.Remove(inspectionIndex) }()
	inspectionContext := context.WithValue(ctx, inspectionIndexContextKey{}, inspectionIndex)
	commands := []struct {
		destination *[]byte
		arguments   []string
	}{
		{arguments: []string{"-c", "core.fileMode=false", "ls-files", "--stage", "-z"}},
		{arguments: []string{"-c", "core.fileMode=false", "ls-files", "-v", "-z"}},
		{arguments: []string{"-c", "core.fileMode=false", "diff", "--cached", "--name-only", "--no-renames", "-z", "--"}},
		{arguments: []string{"-c", "core.fileMode=false", "diff", "--name-only", "--no-renames", "-z", "--"}},
		{arguments: []string{"-c", "core.fileMode=false", "ls-files", "--others", "-z", "--exclude-standard"}},
		{arguments: []string{"-c", "core.fileMode=false", "ls-files", "--others", "--ignored", "-z", "--exclude-standard"}},
	}
	snapshot := repositorySnapshot{head: head}
	commands[0].destination = &snapshot.index
	commands[1].destination = &snapshot.flags
	commands[2].destination = &snapshot.staged
	commands[3].destination = &snapshot.unstaged
	commands[4].destination = &snapshot.untracked
	commands[5].destination = &snapshot.ignored
	for _, command := range commands {
		output, runErr := runner.Run(inspectionContext, root, command.arguments...)
		if runErr != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return repositorySnapshot{}, contextErr
			}
			return repositorySnapshot{}, fmt.Errorf("%w: Git inspection failed: %v", ErrRepository, runErr)
		}
		*command.destination = append([]byte(nil), output...)
	}
	indexEntries, err := parseIndexEntries(snapshot.index)
	if err != nil {
		return repositorySnapshot{}, err
	}
	snapshot.worktree, err = captureWorktreeFingerprint(inspectionContext, root, runner, indexEntries)
	if err != nil {
		return repositorySnapshot{}, err
	}
	rawAfter, err := readRawIndex(ctx, root, runner)
	if err != nil {
		return repositorySnapshot{}, err
	}
	if !rawBefore.equal(rawAfter) {
		return repositorySnapshot{}, fmt.Errorf(
			"%w: raw index changed during inspection (same_file=%t digest_before=%x digest_after=%x size_before=%d size_after=%d mode_before=%v mode_after=%v mtime_before=%d mtime_after=%d)",
			ErrStateChanged,
			rawBefore.Identity != nil && rawAfter.Identity != nil && os.SameFile(rawBefore.Identity, rawAfter.Identity),
			rawBefore.Digest,
			rawAfter.Digest,
			rawBefore.Size,
			rawAfter.Size,
			rawBefore.Mode,
			rawAfter.Mode,
			rawBefore.ModifiedUnixNano,
			rawAfter.ModifiedUnixNano,
		)
	}
	finalHeadOutput, err := runner.Run(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return repositorySnapshot{}, contextErr
		}
		return repositorySnapshot{}, fmt.Errorf("%w: cannot re-read HEAD after repository inspection", ErrStateChanged)
	}
	finalHead := strings.TrimSpace(string(finalHeadOutput))
	if !objectIDPattern.MatchString(finalHead) || finalHead != head {
		return repositorySnapshot{}, fmt.Errorf("%w: HEAD changed during repository inspection", ErrStateChanged)
	}
	snapshot.rawIndex = rawBefore
	snapshot.rawIndex.Data = nil
	return snapshot, nil
}

func captureWorktreeFingerprint(ctx context.Context, root string, runner commandRunner, entries []indexEntry) ([]byte, error) {
	fingerprint := make([]byte, 0, len(entries)*(sha256.Size*2+2))
	for _, entry := range entries {
		if isExcludedTrackedArtifact(entry.path) {
			continue
		}
		output, err := runner.Run(ctx, root, "hash-object", "--path="+entry.path, "--", entry.path)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			return nil, fmt.Errorf("%w: cannot hash tracked worktree path %q: %v", ErrDirty, entry.path, err)
		}
		objectID := strings.TrimSpace(string(output))
		if !objectIDPattern.MatchString(objectID) {
			return nil, fmt.Errorf("%w: Git returned an invalid worktree object ID for %q", ErrRepository, entry.path)
		}
		fingerprint = append(fingerprint, entry.path...)
		fingerprint = append(fingerprint, 0)
		fingerprint = append(fingerprint, objectID...)
		fingerprint = append(fingerprint, 0)
		if objectID != entry.objectID {
			return nil, fmt.Errorf("%w: tracked path %q differs from the index", ErrDirty, entry.path)
		}
	}
	return fingerprint, nil
}

type rawIndexSnapshot struct {
	Digest           [sha256.Size]byte
	Size             int64
	Mode             os.FileMode
	ModifiedUnixNano int64
	Data             []byte
	Identity         os.FileInfo
}

func (snapshot rawIndexSnapshot) equal(other rawIndexSnapshot) bool {
	return snapshot.Identity != nil && other.Identity != nil && os.SameFile(snapshot.Identity, other.Identity) &&
		snapshot.Digest == other.Digest && snapshot.Size == other.Size && snapshot.Mode == other.Mode && snapshot.ModifiedUnixNano == other.ModifiedUnixNano
}

func readRawIndex(ctx context.Context, root string, runner commandRunner) (rawIndexSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return rawIndexSnapshot{}, err
	}
	pathOutput, err := runner.Run(ctx, root, "rev-parse", "--git-path", "index")
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return rawIndexSnapshot{}, contextErr
		}
		return rawIndexSnapshot{}, fmt.Errorf("%w: cannot resolve Git index", ErrRepository)
	}
	indexPath := strings.TrimSpace(string(pathOutput))
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(root, indexPath)
	}
	indexPath = filepath.Clean(indexPath)
	info, err := os.Lstat(indexPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return rawIndexSnapshot{}, fmt.Errorf("%w: Git index is not a regular file", ErrUnsafeInput)
	}
	parent, err := os.OpenRoot(filepath.Dir(indexPath))
	if err != nil {
		return rawIndexSnapshot{}, fmt.Errorf("%w: cannot anchor Git index parent", ErrUnsafeInput)
	}
	defer func() { _ = parent.Close() }()
	anchoredInfo, err := parent.Lstat(filepath.Base(indexPath))
	if err != nil || anchoredInfo.Mode()&os.ModeSymlink != 0 || !anchoredInfo.Mode().IsRegular() || !os.SameFile(info, anchoredInfo) {
		return rawIndexSnapshot{}, fmt.Errorf("%w: Git index changed before open", ErrStateChanged)
	}
	file, err := parent.Open(filepath.Base(indexPath))
	if err != nil {
		return rawIndexSnapshot{}, fmt.Errorf("%w: cannot open Git index", ErrUnsafeInput)
	}
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return rawIndexSnapshot{}, fmt.Errorf("%w: Git index changed while opening", ErrStateChanged)
	}
	const maximumIndexBytes = 128 << 20
	data, err := io.ReadAll(io.LimitReader(file, maximumIndexBytes+1))
	if err != nil {
		return rawIndexSnapshot{}, fmt.Errorf("%w: cannot read Git index", ErrRepository)
	}
	if len(data) > maximumIndexBytes {
		return rawIndexSnapshot{}, fmt.Errorf("%w: Git index exceeds inspection limit", ErrUnsafeInput)
	}
	if err := ctx.Err(); err != nil {
		return rawIndexSnapshot{}, err
	}
	afterInfo, err := parent.Lstat(filepath.Base(indexPath))
	if err != nil {
		return rawIndexSnapshot{}, fmt.Errorf("%w: Git index disappeared during read", ErrStateChanged)
	}
	if !os.SameFile(info, afterInfo) || afterInfo.Size() != openedInfo.Size() || !afterInfo.ModTime().Equal(openedInfo.ModTime()) {
		return rawIndexSnapshot{}, fmt.Errorf(
			"%w: Git index changed during read (same_file=%t size_before=%d size_after=%d mtime_before=%d mtime_after=%d)",
			ErrStateChanged,
			os.SameFile(info, afterInfo),
			openedInfo.Size(),
			afterInfo.Size(),
			openedInfo.ModTime().UnixNano(),
			afterInfo.ModTime().UnixNano(),
		)
	}
	return rawIndexSnapshot{
		Digest:           sha256.Sum256(data),
		Size:             openedInfo.Size(),
		Mode:             openedInfo.Mode(),
		ModifiedUnixNano: openedInfo.ModTime().UnixNano(),
		Data:             data,
		Identity:         openedInfo,
	}, nil
}

func createInspectionIndex(data []byte) (string, error) {
	file, err := os.CreateTemp("", "whiteboard-git-index-*")
	if err != nil {
		return "", fmt.Errorf("%w: create private Git index snapshot: %v", ErrRepository, err)
	}
	name := file.Name()
	remove := func() {
		_ = file.Close()
		_ = os.Remove(name)
	}
	written, err := file.Write(data)
	if err != nil {
		remove()
		return "", fmt.Errorf("%w: write private Git index snapshot: %v", ErrRepository, err)
	}
	if written != len(data) {
		remove()
		return "", fmt.Errorf("%w: short write of private Git index snapshot", ErrRepository)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("%w: close private Git index snapshot: %v", ErrRepository, err)
	}
	return name, nil
}

func validateSnapshot(root string, snapshot repositorySnapshot) error {
	if err := validateDirtyPaths(snapshot.staged); err != nil {
		return err
	}
	if err := validateDirtyPaths(snapshot.unstaged); err != nil {
		return err
	}
	seen := make(map[string]struct{})
	for _, source := range [][]byte{snapshot.untracked, snapshot.ignored} {
		paths, err := splitNULPaths(source)
		if err != nil {
			return err
		}
		for _, path := range paths {
			if _, exists := seen[path]; exists {
				continue
			}
			seen[path] = struct{}{}
			if !isAllowedUntrackedArtifact(path) {
				return fmt.Errorf("%w: untracked path %q is not an allowed artifact", ErrDirty, path)
			}
			info, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
			if statErr != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("%w: untracked artifact %q is not a regular file", ErrUnsafeInput, path)
			}
		}
	}
	return nil
}

func validateDirtyPaths(data []byte) error {
	paths, err := splitNULPaths(data)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if !isExcludedTrackedArtifact(path) {
			return fmt.Errorf("%w: tracked path %q differs from HEAD", ErrDirty, path)
		}
	}
	return nil
}

type indexEntry struct {
	path     string
	mode     os.FileMode
	objectID string
}

func validateTrackedArtifacts(root string, entries []indexEntry) error {
	for _, entry := range entries {
		if !isExcludedTrackedArtifact(entry.path) {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.path)))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: tracked artifact %q is not a regular worktree file", ErrUnsafeInput, entry.path)
		}
	}
	return nil
}

func parseIndexEntries(data []byte) ([]indexEntry, error) {
	records := bytes.Split(data, []byte{0})
	entries := make([]indexEntry, 0, len(records))
	seen := make(map[string]struct{})
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		tab := bytes.IndexByte(record, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("%w: malformed index record", ErrRepository)
		}
		fields := strings.Fields(string(record[:tab]))
		if len(fields) != 3 {
			return nil, fmt.Errorf("%w: malformed index header", ErrRepository)
		}
		path := string(record[tab+1:])
		if !validInputPath(path) {
			return nil, fmt.Errorf("%w: invalid index path %q", ErrUnsafeInput, path)
		}
		if fields[2] != "0" {
			return nil, fmt.Errorf("%w: path %q has nonzero index stage", ErrUnsafeInput, path)
		}
		var mode os.FileMode
		switch fields[0] {
		case "100644":
			mode = 0o644
		case "100755":
			mode = 0o755
		default:
			return nil, fmt.Errorf("%w: path %q has unsupported Git mode %s", ErrUnsafeInput, path, fields[0])
		}
		if !objectIDPattern.MatchString(fields[1]) {
			return nil, fmt.Errorf("%w: path %q has invalid object ID", ErrUnsafeInput, path)
		}
		if _, exists := seen[path]; exists {
			return nil, fmt.Errorf("%w: duplicate index path %q", ErrUnsafeInput, path)
		}
		seen[path] = struct{}{}
		if strings.HasPrefix(path, ".superpowers/") {
			return nil, fmt.Errorf("%w: tracked tool state %q is forbidden", ErrUnsafeInput, path)
		}
		if hasArtifactRoot(path) && !isExcludedTrackedArtifact(path) {
			return nil, fmt.Errorf("%w: unknown tracked artifact path %q", ErrUnsafeInput, path)
		}
		entries = append(entries, indexEntry{path: path, mode: mode, objectID: fields[1]})
	}
	return entries, nil
}

func validateIndexFlags(data []byte, entries []indexEntry) error {
	records := bytes.Split(data, []byte{0})
	flags := make(map[string]byte, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		if len(record) < 3 || record[1] != ' ' || !validInputPath(string(record[2:])) {
			return fmt.Errorf("%w: malformed index flag record", ErrRepository)
		}
		path := string(record[2:])
		if _, exists := flags[path]; exists {
			return fmt.Errorf("%w: duplicate index flag for %q", ErrUnsafeInput, path)
		}
		flags[path] = record[0]
	}
	for _, entry := range entries {
		if flags[entry.path] != 'H' {
			return fmt.Errorf("%w: path %q has sparse/skip-worktree/assume-unchanged state", ErrUnsafeInput, entry.path)
		}
	}
	return nil
}

func splitNULPaths(data []byte) ([]string, error) {
	records := bytes.Split(data, []byte{0})
	paths := make([]string, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		path := string(record)
		if !validInputPath(path) {
			return nil, fmt.Errorf("%w: invalid untracked path %q", ErrUnsafeInput, path)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func hasArtifactRoot(path string) bool {
	return strings.HasPrefix(path, "evidence/") || strings.HasPrefix(path, "generated/") || strings.HasPrefix(path, ".superpowers/")
}

func isExcludedTrackedArtifact(path string) bool {
	if path == "evidence/runs/.gitkeep" || path == "evidence/releases/.gitkeep" ||
		path == "generated/.gitkeep" || path == "generated/coverage.md" ||
		path == "generated/.bin/whiteboard" || path == "generated/.verify/report.json" {
		return true
	}
	if strings.HasPrefix(path, "evidence/runs/") {
		return validRunFileName(strings.TrimPrefix(path, "evidence/runs/"))
	}
	if strings.HasPrefix(path, "evidence/releases/") {
		return releasePattern.MatchString(strings.TrimPrefix(path, "evidence/releases/"))
	}
	return false
}

func validRunFileName(name string) bool {
	if !runFilePattern.MatchString(name) {
		return false
	}
	const prefix = "run-"
	const timestampLength = len("20060102T150405.000Z")
	timestamp := name[len(prefix) : len(prefix)+timestampLength]
	parsed, err := time.Parse("20060102T150405.000Z", timestamp)
	return err == nil && parsed.Format("20060102T150405.000Z") == timestamp
}

func isAllowedUntrackedArtifact(path string) bool {
	if isExcludedTrackedArtifact(path) {
		return true
	}
	if !strings.HasPrefix(path, ".superpowers/sdd/") {
		return false
	}
	name := strings.TrimPrefix(path, ".superpowers/sdd/")
	return name != "" && !strings.ContainsRune(name, '/') && (strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".diff"))
}
