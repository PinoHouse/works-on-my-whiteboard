package inputdigest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestComputeStateHashesIndexBlobsAndReturnsHEAD(t *testing.T) {
	repo := newGitFixture(t)
	repo.write("README.md", []byte("hello\n"), 0o644)
	repo.write("scripts/run.sh", []byte("#!/bin/sh\nexit 0\n"), 0o755)
	repo.commitAll("initial")

	state, err := ComputeState(context.Background(), repo.root)
	if err != nil {
		t.Fatalf("ComputeState: %v", err)
	}
	if state.SourceCommit != strings.TrimSpace(repo.git("rev-parse", "HEAD")) {
		t.Fatalf("source commit = %q", state.SourceCommit)
	}

	entries := repo.indexEntries()
	want, err := ComputeEntries(entries)
	if err != nil {
		t.Fatalf("ComputeEntries: %v", err)
	}
	if state.InputDigest != want {
		t.Fatalf("input digest = %q, want index digest %q", state.InputDigest, want)
	}
	const frozen = Digest("sha256:7985c74be03783034436e0c7ee76f9956016f151ceca6e94770c59575ac2a332")
	if state.InputDigest != frozen {
		t.Fatalf("repository digest = %q, want frozen %q", state.InputDigest, frozen)
	}
	got, err := Compute(context.Background(), repo.root)
	if err != nil || got != state.InputDigest {
		t.Fatalf("Compute = %q, %v; want %q", got, err, state.InputDigest)
	}
}

func TestComputeStateRejectsRepositoryAuthorityFailures(t *testing.T) {
	t.Run("nil context", func(t *testing.T) {
		repo := newGitFixture(t)
		repo.write("a", []byte("a"), 0o644)
		repo.commitAll("initial")
		_, err := ComputeState(nil, repo.root)
		if !errors.Is(err, ErrRepository) {
			t.Fatalf("error = %v, want ErrRepository", err)
		}
	})
	t.Run("subdirectory", func(t *testing.T) {
		repo := newGitFixture(t)
		repo.write("sub/a", []byte("a"), 0o644)
		repo.commitAll("initial")
		_, err := ComputeState(context.Background(), filepath.Join(repo.root, "sub"))
		if !errors.Is(err, ErrRepository) {
			t.Fatalf("error = %v, want ErrRepository", err)
		}
	})
	t.Run("unborn", func(t *testing.T) {
		repo := newGitFixture(t)
		_, err := ComputeState(context.Background(), repo.root)
		if !errors.Is(err, ErrRepository) {
			t.Fatalf("error = %v, want ErrRepository", err)
		}
	})
	t.Run("bare", func(t *testing.T) {
		root := t.TempDir()
		runCommand(t, root, nil, "git", "init", "--bare", "--quiet", ".")
		_, err := ComputeState(context.Background(), root)
		if !errors.Is(err, ErrRepository) {
			t.Fatalf("error = %v, want ErrRepository", err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		repo := newGitFixture(t)
		repo.write("a", []byte("a"), 0o644)
		repo.commitAll("initial")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := ComputeState(ctx, repo.root)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	})
}

func TestComputeStateRejectsMeaningfulDirtyAndUntrackedInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*gitFixture)
	}{
		{name: "unstaged", mutate: func(repo *gitFixture) { repo.write("a.go", []byte("changed"), 0o644) }},
		{name: "staged", mutate: func(repo *gitFixture) { repo.write("a.go", []byte("changed"), 0o644); repo.git("add", "a.go") }},
		{name: "untracked", mutate: func(repo *gitFixture) { repo.write("new.go", []byte("package p"), 0o644) }},
		{name: "ignored", mutate: func(repo *gitFixture) {
			repo.write("ignored.go", []byte("package p"), 0o644)
		}},
		{name: "generated source injection", mutate: func(repo *gitFixture) { repo.write("generated/evil.go", []byte("package evil"), 0o644) }},
		{name: "evidence source injection", mutate: func(repo *gitFixture) { repo.write("evidence/runs/evil.go", []byte("package evil"), 0o644) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newGitFixture(t)
			repo.write(".gitignore", []byte("ignored.go\n.superpowers/\ngenerated/\nevidence/\n"), 0o644)
			repo.write("a.go", []byte("package a\n"), 0o644)
			repo.commitAll("initial")
			test.mutate(repo)
			_, err := ComputeState(context.Background(), repo.root)
			if !errors.Is(err, ErrDirty) && !errors.Is(err, ErrUnsafeInput) {
				t.Fatalf("error = %v, want dirty/unsafe", err)
			}
		})
	}
}

func TestComputeStateAllowsOnlyExactArtifactWhitelist(t *testing.T) {
	repo := newGitFixture(t)
	repo.write(".gitignore", []byte(".superpowers/\ngenerated/\nevidence/\n"), 0o644)
	repo.write("a.go", []byte("package a\n"), 0o644)
	repo.commitAll("initial")

	allowed := map[string][]byte{
		".superpowers/sdd/task-8-report.md":                                                                       []byte("local"),
		".superpowers/sdd/task-8-review.diff":                                                                     []byte("local"),
		"evidence/runs/run-20260714T120000.000Z-0123456789abcdef0123456789abcdef.json":                            []byte("{}\n"),
		"evidence/releases/sha256-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/manifest.yaml": []byte("schema_version: 1\n"),
		"generated/coverage.md":                                                                                   []byte("generated"),
		"generated/.bin/whiteboard":                                                                               []byte("binary"),
		"generated/.verify/report.json":                                                                           []byte("{}\n"),
	}
	for path, data := range allowed {
		mode := os.FileMode(0o644)
		if path == "generated/.bin/whiteboard" {
			mode = 0o755
		}
		repo.write(path, data, mode)
	}
	if _, err := ComputeState(context.Background(), repo.root); err != nil {
		t.Fatalf("allowed artifacts rejected: %v", err)
	}

	repo.write(".superpowers/sdd/nested/evil.md", []byte("evil"), 0o644)
	if _, err := ComputeState(context.Background(), repo.root); !errors.Is(err, ErrUnsafeInput) && !errors.Is(err, ErrDirty) {
		t.Fatalf("nested tool state error = %v", err)
	}
}

func TestComputeStateRejectsArtifactRunNameWithImpossibleTimestamp(t *testing.T) {
	repo := newGitFixture(t)
	repo.write(".gitignore", []byte("evidence/\n"), 0o644)
	repo.write("a.go", []byte("package a\n"), 0o644)
	repo.commitAll("initial")
	repo.write("evidence/runs/run-20269999T999999.999Z-0123456789abcdef0123456789abcdef.json", []byte("{}\n"), 0o644)
	if _, err := ComputeState(context.Background(), repo.root); !errors.Is(err, ErrDirty) && !errors.Is(err, ErrUnsafeInput) {
		t.Fatalf("impossible timestamp artifact error = %v, want dirty/unsafe", err)
	}
}

func TestComputeStateUsesIndexContentAcrossWorktreeTransformsAndHostChmod(t *testing.T) {
	repo := newGitFixture(t)
	repo.write(".gitattributes", []byte("text.txt text eol=lf\n"), 0o644)
	repo.write("text.txt", []byte("line1\nline2\n"), 0o644)
	repo.write("script.sh", []byte("#!/bin/sh\n"), 0o644)
	repo.commitAll("initial")
	want, err := ComputeState(context.Background(), repo.root)
	if err != nil {
		t.Fatalf("initial state: %v", err)
	}

	repo.git("config", "core.autocrlf", "true")
	repo.write("text.txt", []byte("line1\r\nline2\r\n"), 0o644)
	if runtime.GOOS != "windows" {
		if chmodErr := os.Chmod(filepath.Join(repo.root, "script.sh"), 0o755); chmodErr != nil {
			t.Fatalf("chmod: %v", chmodErr)
		}
	}
	got, err := ComputeState(context.Background(), repo.root)
	if err != nil {
		t.Fatalf("transformed worktree state: %v", err)
	}
	if got.InputDigest != want.InputDigest {
		t.Fatalf("worktree transform changed digest: got %q want %q", got.InputDigest, want.InputDigest)
	}
}

func TestComputeStateIgnoresLocalReplaceRefs(t *testing.T) {
	t.Run("indexed blob", func(t *testing.T) {
		repo := committedOneFileRepo(t)
		want, err := ComputeState(context.Background(), repo.root)
		if err != nil {
			t.Fatalf("initial state: %v", err)
		}
		originalBlob := strings.TrimSpace(repo.git("rev-parse", ":a"))
		replacementBlob := strings.TrimSpace(string(runCommand(t, repo.root, []byte("replacement\n"), "git", "hash-object", "-w", "--stdin")))
		repo.git("replace", originalBlob, replacementBlob)
		if got := repo.git("cat-file", "blob", originalBlob); got != "replacement\n" {
			t.Fatalf("replace ref precondition failed: cat-file returned %q", got)
		}

		got, err := ComputeState(context.Background(), repo.root)
		if err != nil {
			t.Fatalf("state with blob replace ref: %v", err)
		}
		if got != want {
			t.Fatalf("blob replace ref changed state: got %+v want %+v", got, want)
		}
	})

	t.Run("HEAD commit", func(t *testing.T) {
		repo := committedOneFileRepo(t)
		want, err := ComputeState(context.Background(), repo.root)
		if err != nil {
			t.Fatalf("initial state: %v", err)
		}
		originalCommit := want.SourceCommit
		repo.write("a", []byte("replacement\n"), 0o644)
		repo.commitAll("replacement")
		replacementCommit := strings.TrimSpace(repo.git("rev-parse", "HEAD"))
		repo.git("reset", "--hard", "--quiet", originalCommit)
		repo.git("replace", originalCommit, replacementCommit)
		if got := repo.git("show", "HEAD:a"); got != "replacement\n" {
			t.Fatalf("replace ref precondition failed: show returned %q", got)
		}

		got, err := ComputeState(context.Background(), repo.root)
		if err != nil {
			t.Fatalf("state with commit replace ref: %v", err)
		}
		if got != want {
			t.Fatalf("commit replace ref changed state: got %+v want %+v", got, want)
		}
	})
}

func TestComputeStateRejectsSameStatWorktreeRewrite(t *testing.T) {
	repo := newGitFixture(t)
	repo.write("a", []byte("alpha\n"), 0o644)
	repo.commitAll("initial")
	path := filepath.Join(repo.root, "a")
	oldTime := time.Unix(946684800, 0)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatalf("age tracked file: %v", err)
	}
	repo.git("update-index", "--really-refresh")
	committedInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat committed file: %v", err)
	}
	repo.git("config", "core.trustctime", "false")
	repo.git("config", "core.checkStat", "minimal")
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open tracked file: %v", err)
	}
	if _, err := file.WriteAt([]byte("omega\n"), 0); err != nil {
		t.Fatalf("rewrite tracked file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close tracked file: %v", err)
	}
	if err := os.Chtimes(path, committedInfo.ModTime(), committedInfo.ModTime()); err != nil {
		t.Fatalf("restore tracked file mtime: %v", err)
	}
	if output := repo.git("diff", "--name-only", "--", "a"); output != "" {
		t.Skipf("filesystem/Git cannot reproduce same-stat bypass: diff returned %q", output)
	}

	_, err = ComputeState(context.Background(), repo.root)
	if !errors.Is(err, ErrDirty) && !errors.Is(err, ErrStateChanged) {
		t.Fatalf("same-stat rewrite error = %v, want ErrDirty or ErrStateChanged", err)
	}
}

func TestComputeStateRejectsStagedModeUntilCommitThenChangesDigest(t *testing.T) {
	repo := newGitFixture(t)
	repo.write("script.sh", []byte("#!/bin/sh\n"), 0o644)
	repo.commitAll("initial")
	before, err := ComputeState(context.Background(), repo.root)
	if err != nil {
		t.Fatalf("initial state: %v", err)
	}
	repo.git("update-index", "--chmod=+x", "script.sh")
	if _, err := ComputeState(context.Background(), repo.root); !errors.Is(err, ErrDirty) {
		t.Fatalf("staged mode error = %v, want ErrDirty", err)
	}
	repo.git("commit", "--quiet", "-m", "mode")
	after, err := ComputeState(context.Background(), repo.root)
	if err != nil {
		t.Fatalf("committed mode state: %v", err)
	}
	if after.InputDigest == before.InputDigest {
		t.Fatalf("committed executable bit retained digest %q", after.InputDigest)
	}
}

func TestComputeStateRejectsUnsupportedIndexEntriesAndFlags(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		repo := newGitFixture(t)
		repo.write("target", []byte("x"), 0o644)
		if err := os.Symlink("target", filepath.Join(repo.root, "link")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		repo.commitAll("symlink")
		_, err := ComputeState(context.Background(), repo.root)
		if !errors.Is(err, ErrUnsafeInput) {
			t.Fatalf("error = %v, want ErrUnsafeInput", err)
		}
	})
	t.Run("assume unchanged", func(t *testing.T) {
		repo := committedOneFileRepo(t)
		repo.git("update-index", "--assume-unchanged", "a")
		_, err := ComputeState(context.Background(), repo.root)
		if !errors.Is(err, ErrUnsafeInput) {
			t.Fatalf("error = %v, want ErrUnsafeInput", err)
		}
	})
	t.Run("skip worktree", func(t *testing.T) {
		repo := committedOneFileRepo(t)
		repo.git("update-index", "--skip-worktree", "a")
		_, err := ComputeState(context.Background(), repo.root)
		if !errors.Is(err, ErrUnsafeInput) {
			t.Fatalf("error = %v, want ErrUnsafeInput", err)
		}
	})
	t.Run("unmerged", func(t *testing.T) {
		repo := committedOneFileRepo(t)
		oid := strings.TrimSpace(repo.git("hash-object", "a"))
		indexInfo := fmt.Sprintf("100644 %s 1\ta\n100644 %s 2\ta\n", oid, oid)
		runCommand(t, repo.root, []byte(indexInfo), "git", "update-index", "--index-info")
		_, err := ComputeState(context.Background(), repo.root)
		if !errors.Is(err, ErrUnsafeInput) {
			t.Fatalf("error = %v, want ErrUnsafeInput", err)
		}
	})
	t.Run("submodule", func(t *testing.T) {
		child := committedOneFileRepo(t)
		parent := committedOneFileRepo(t)
		parent.git("-c", "protocol.file.allow=always", "submodule", "add", "--quiet", child.root, "child")
		parent.commitAll("submodule")
		_, err := ComputeState(context.Background(), parent.root)
		if !errors.Is(err, ErrUnsafeInput) {
			t.Fatalf("error = %v, want ErrUnsafeInput", err)
		}
	})
}

func TestComputeStateHandlesTABAndLFInTrackedFilename(t *testing.T) {
	repo := newGitFixture(t)
	repo.write("tab\tline\nname", []byte("data"), 0o644)
	repo.commitAll("weird path")
	if _, err := ComputeState(context.Background(), repo.root); err != nil {
		t.Fatalf("ComputeState: %v", err)
	}
}

func TestComputeStateRejectsTrackedFilesInsideExcludedRoots(t *testing.T) {
	repo := newGitFixture(t)
	repo.write("generated/evil.go", []byte("package evil\n"), 0o644)
	repo.commitAll("hidden source")
	if _, err := ComputeState(context.Background(), repo.root); !errors.Is(err, ErrUnsafeInput) {
		t.Fatalf("error = %v, want ErrUnsafeInput", err)
	}
}

func TestComputeStateRejectsTrackedArtifactReplacedByWorktreeSymlink(t *testing.T) {
	repo := newGitFixture(t)
	repo.write("a.go", []byte("package a\n"), 0o644)
	repo.write("evidence/runs/.gitkeep", []byte{}, 0o644)
	repo.commitAll("tracked marker")
	marker := filepath.Join(repo.root, "evidence", "runs", ".gitkeep")
	if err := os.Remove(marker); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	if err := os.Symlink(filepath.Join(repo.root, "a.go"), marker); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ComputeState(context.Background(), repo.root); !errors.Is(err, ErrUnsafeInput) {
		t.Fatalf("error = %v, want ErrUnsafeInput", err)
	}
}

func TestComputeStateRevalidatesArtifactTypesAfterBlobReads(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*gitFixture)
	}{
		{name: "untracked allowed artifact", prepare: func(repo *gitFixture) {
			repo.write("a", []byte("source\n"), 0o644)
			repo.commitAll("source")
			repo.write("generated/coverage.md", []byte("coverage\n"), 0o644)
		}},
		{name: "tracked dirty allowed artifact", prepare: func(repo *gitFixture) {
			repo.write("a", []byte("source\n"), 0o644)
			repo.write("generated/coverage.md", []byte("old coverage\n"), 0o644)
			repo.commitAll("source and artifact")
			repo.write("generated/coverage.md", []byte("new coverage\n"), 0o644)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newGitFixture(t)
			test.prepare(repo)
			runner := &mutatingCommandRunner{delegate: systemGitRunner{}, mutate: func() {
				artifact := filepath.Join(repo.root, "generated", "coverage.md")
				if err := os.Remove(artifact); err != nil {
					t.Fatalf("remove artifact: %v", err)
				}
				if err := os.Symlink(filepath.Join(repo.root, "a"), artifact); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}}
			if _, err := computeState(context.Background(), repo.root, runner); !errors.Is(err, ErrUnsafeInput) && !errors.Is(err, ErrStateChanged) {
				t.Fatalf("error = %v, want ErrUnsafeInput or ErrStateChanged", err)
			}
		})
	}
}

func TestComputeStateRejectsConcurrentIndexMutation(t *testing.T) {
	t.Run("HEAD", func(t *testing.T) {
		repo := committedOneFileRepo(t)
		runner := &mutatingCommandRunner{delegate: systemGitRunner{}, mutate: func() {
			repo.write("b", []byte("b\n"), 0o644)
			repo.commitAll("concurrent commit")
		}}
		_, err := computeState(context.Background(), repo.root, runner)
		if !errors.Is(err, ErrStateChanged) {
			t.Fatalf("error = %v, want ErrStateChanged", err)
		}
	})
	t.Run("semantic mode", func(t *testing.T) {
		repo := committedOneFileRepo(t)
		runner := &mutatingCommandRunner{delegate: systemGitRunner{}, mutate: func() {
			repo.git("update-index", "--chmod=+x", "a")
		}}
		_, err := computeState(context.Background(), repo.root, runner)
		if !errors.Is(err, ErrStateChanged) {
			t.Fatalf("error = %v, want ErrStateChanged", err)
		}
	})
	t.Run("raw index format only", func(t *testing.T) {
		repo := committedOneFileRepo(t)
		repo.git("update-index", "--index-version=2")
		runner := &mutatingCommandRunner{delegate: systemGitRunner{}, mutate: func() {
			repo.git("update-index", "--index-version=4")
		}}
		_, err := computeState(context.Background(), repo.root, runner)
		if !errors.Is(err, ErrStateChanged) {
			t.Fatalf("error = %v, want ErrStateChanged", err)
		}
	})
	t.Run("raw index mtime only", func(t *testing.T) {
		repo := committedOneFileRepo(t)
		runner := &mutatingCommandRunner{delegate: systemGitRunner{}, mutate: func() {
			indexPath := filepath.Join(repo.root, ".git", "index")
			info, err := os.Stat(indexPath)
			if err != nil {
				t.Fatalf("stat index: %v", err)
			}
			if err := os.Chtimes(indexPath, info.ModTime(), info.ModTime().Add(2*time.Hour)); err != nil {
				t.Fatalf("change index mtime: %v", err)
			}
		}}
		_, err := computeState(context.Background(), repo.root, runner)
		if !errors.Is(err, ErrStateChanged) {
			t.Fatalf("error = %v, want ErrStateChanged", err)
		}
	})
	t.Run("raw index identity only", func(t *testing.T) {
		repo := committedOneFileRepo(t)
		runner := &mutatingCommandRunner{delegate: systemGitRunner{}, mutate: func() {
			indexPath := filepath.Join(repo.root, ".git", "index")
			data, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatalf("read index: %v", err)
			}
			info, err := os.Stat(indexPath)
			if err != nil {
				t.Fatalf("stat index: %v", err)
			}
			replacement := indexPath + ".replacement"
			if err := os.WriteFile(replacement, data, info.Mode().Perm()); err != nil {
				t.Fatalf("write replacement: %v", err)
			}
			if err := os.Chmod(replacement, info.Mode().Perm()); err != nil {
				t.Fatalf("chmod replacement: %v", err)
			}
			if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
				t.Fatalf("set replacement mtime: %v", err)
			}
			if err := os.Rename(replacement, indexPath); err != nil {
				t.Fatalf("replace index: %v", err)
			}
		}}
		_, err := computeState(context.Background(), repo.root, runner)
		if !errors.Is(err, ErrStateChanged) {
			t.Fatalf("error = %v, want ErrStateChanged", err)
		}
	})
}

func TestRepositorySnapshotReadsHEADAgainAfterAllIndexAndStatusMetadata(t *testing.T) {
	repo := committedOneFileRepo(t)
	runner := &afterCommandRunner{
		delegate: systemGitRunner{},
		match:    isFinalStatusCommand,
		mutate: func() {
			repo.git("commit", "--quiet", "--allow-empty", "-m", "concurrent empty commit")
		},
	}
	if _, err := captureRepositorySnapshotOnce(context.Background(), repo.root, runner); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("snapshot error = %v, want ErrStateChanged", err)
	}
}

func TestRepositorySnapshotNeverRetriesAwayOneTimeMutation(t *testing.T) {
	repo := committedOneFileRepo(t)
	repo.git("update-index", "--index-version=2")
	runner := &afterCommandRunner{
		delegate: systemGitRunner{},
		match:    isFinalStatusCommand,
		mutate: func() {
			repo.git("update-index", "--index-version=4")
		},
	}
	if _, err := captureRepositorySnapshot(context.Background(), repo.root, runner); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("snapshot error = %v, want ErrStateChanged", err)
	}
}

func TestComputeStateHashesWorktreeAfterSecondUnstagedDiff(t *testing.T) {
	repo := committedOneFileRepo(t)
	runner := &afterNthCommandRunner{
		delegate: systemGitRunner{},
		match:    isUnstagedDiffCommand,
		after:    2,
		mutate: func() {
			repo.write("a", []byte("mutated after diff\n"), 0o644)
		},
	}
	_, err := computeState(context.Background(), repo.root, runner)
	if !errors.Is(err, ErrDirty) && !errors.Is(err, ErrStateChanged) {
		t.Fatalf("post-diff worktree mutation error = %v, want ErrDirty or ErrStateChanged", err)
	}
	if runner.seen != runner.after {
		t.Fatalf("unstaged diff matches = %d, want %d", runner.seen, runner.after)
	}
}

func TestRawIndexSnapshotEqualityIncludesMtime(t *testing.T) {
	repo := committedOneFileRepo(t)
	identity, err := os.Stat(filepath.Join(repo.root, ".git", "index"))
	if err != nil {
		t.Fatalf("stat index: %v", err)
	}
	left := rawIndexSnapshot{Digest: [32]byte{1}, Size: 10, Mode: 0o644, ModifiedUnixNano: 1, Identity: identity}
	right := left
	right.ModifiedUnixNano = 2
	if left.equal(right) {
		t.Fatal("raw index snapshots with different mtimes compared equal")
	}
}

func TestComputeStateSanitizesHostileGitEnvironment(t *testing.T) {
	good := committedOneFileRepo(t)
	evil := committedOneFileRepo(t)
	goodHead := strings.TrimSpace(good.git("rev-parse", "HEAD"))
	t.Setenv("GIT_DIR", filepath.Join(evil.root, ".git"))
	t.Setenv("GIT_WORK_TREE", evil.root)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(evil.root, ".git", "index"))
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(evil.root, ".git", "objects"))
	t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", filepath.Join(evil.root, ".git", "objects"))
	t.Setenv("GIT_COMMON_DIR", filepath.Join(evil.root, ".git"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.worktree")
	t.Setenv("GIT_CONFIG_VALUE_0", evil.root)

	state, err := ComputeState(context.Background(), good.root)
	if err != nil {
		t.Fatalf("ComputeState: %v", err)
	}
	if state.SourceCommit != goodHead {
		t.Fatalf("hostile environment redirected source commit to %q", state.SourceCommit)
	}
}

func TestSanitizedGitEnvironmentForcesReplaceRefsOff(t *testing.T) {
	environment := sanitizedGitEnvironment([]string{
		"PATH=/usr/bin",
		"GIT_DIR=/attacker",
		"GIT_NO_REPLACE_OBJECTS=0",
		"LC_ALL=attacker",
	})
	want := []string{
		"PATH=/usr/bin",
		"LC_ALL=C",
		"LANG=C",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_REPLACE_OBJECTS=1",
	}
	if !slices.Equal(environment, want) {
		t.Fatalf("sanitized environment = %q, want %q", environment, want)
	}
}

type gitFixture struct {
	t    *testing.T
	root string
}

type mutatingCommandRunner struct {
	delegate commandRunner
	mutate   func()
	mutated  bool
}

type afterCommandRunner struct {
	delegate commandRunner
	match    func([]string) bool
	mutate   func()
	mutated  bool
}

type afterNthCommandRunner struct {
	delegate commandRunner
	match    func([]string) bool
	after    int
	mutate   func()
	seen     int
}

func (runner *afterCommandRunner) Run(ctx context.Context, root string, arguments ...string) ([]byte, error) {
	output, err := runner.delegate.Run(ctx, root, arguments...)
	if err == nil && !runner.mutated && runner.match(arguments) {
		runner.mutated = true
		runner.mutate()
	}
	return output, err
}

func (runner *afterNthCommandRunner) Run(ctx context.Context, root string, arguments ...string) ([]byte, error) {
	output, err := runner.delegate.Run(ctx, root, arguments...)
	if err == nil && runner.match(arguments) {
		runner.seen++
		if runner.seen == runner.after {
			runner.mutate()
		}
	}
	return output, err
}

func isFinalStatusCommand(arguments []string) bool {
	return len(arguments) == 7 && arguments[0] == "-c" && arguments[1] == "core.fileMode=false" &&
		arguments[2] == "ls-files" && arguments[3] == "--others" && arguments[4] == "--ignored" &&
		arguments[5] == "-z" && arguments[6] == "--exclude-standard"
}

func isUnstagedDiffCommand(arguments []string) bool {
	want := []string{"-c", "core.fileMode=false", "diff", "--name-only", "--no-renames", "-z", "--"}
	return slices.Equal(arguments, want)
}

func (runner *mutatingCommandRunner) Run(ctx context.Context, root string, arguments ...string) ([]byte, error) {
	output, err := runner.delegate.Run(ctx, root, arguments...)
	if !runner.mutated && len(arguments) >= 2 && arguments[0] == "cat-file" && arguments[1] == "blob" {
		runner.mutated = true
		runner.mutate()
	}
	return output, err
}

func newGitFixture(t *testing.T) *gitFixture {
	t.Helper()
	root := t.TempDir()
	runCommand(t, root, nil, "git", "init", "--quiet", ".")
	runCommand(t, root, nil, "git", "config", "user.name", "Test")
	runCommand(t, root, nil, "git", "config", "user.email", "test@example.com")
	return &gitFixture{t: t, root: root}
}

func committedOneFileRepo(t *testing.T) *gitFixture {
	t.Helper()
	repo := newGitFixture(t)
	repo.write("a", []byte("a\n"), 0o644)
	repo.commitAll("initial")
	return repo
}

func (repo *gitFixture) write(path string, data []byte, mode os.FileMode) {
	repo.t.Helper()
	full := filepath.Join(repo.root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		repo.t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(full, data, mode); err != nil {
		repo.t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(full, mode); err != nil {
		repo.t.Fatalf("chmod %s: %v", path, err)
	}
}

func (repo *gitFixture) commitAll(message string) {
	repo.t.Helper()
	repo.git("add", "-A")
	repo.git("commit", "--quiet", "-m", message)
}

func (repo *gitFixture) git(args ...string) string {
	repo.t.Helper()
	return string(runCommand(repo.t, repo.root, nil, "git", args...))
}

func (repo *gitFixture) indexEntries() []Entry {
	repo.t.Helper()
	output := bytes.TrimSuffix(runCommand(repo.t, repo.root, nil, "git", "ls-files", "--stage", "-z"), []byte{0})
	if len(output) == 0 {
		return []Entry{}
	}
	records := bytes.Split(output, []byte{0})
	entries := make([]Entry, 0, len(records))
	for _, record := range records {
		tab := bytes.IndexByte(record, '\t')
		if tab < 0 {
			repo.t.Fatalf("invalid index record %q", record)
		}
		fields := strings.Fields(string(record[:tab]))
		if len(fields) != 3 {
			repo.t.Fatalf("invalid index header %q", record[:tab])
		}
		mode := os.FileMode(0o644)
		if fields[0] == "100755" {
			mode = 0o755
		}
		data := runCommand(repo.t, repo.root, nil, "git", "cat-file", "blob", fields[1])
		entries = append(entries, Entry{Path: string(record[tab+1:]), Mode: mode, Data: data})
	}
	return entries
}

func runCommand(t *testing.T, directory string, stdin []byte, name string, args ...string) []byte {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	command.Stdin = bytes.NewReader(stdin)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
	return output
}
