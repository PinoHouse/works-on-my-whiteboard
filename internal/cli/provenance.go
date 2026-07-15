package cli

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
)

const (
	codeToolRevisionUnavailable       = "tool_revision_unavailable"
	codeToolRevisionMismatch          = "tool_revision_mismatch"
	codeReleaseInputDigestMismatch    = "release_input_digest_mismatch"
	messageToolRevisionUnavailable    = "executable build provenance is unavailable"
	messageReleaseInputDigestMismatch = "requested release does not match the current input digest"
)

type provenanceIssue struct {
	Code    string
	Message string
}

type provenanceDependencies struct {
	computeState  func(context.Context, string) (inputdigest.State, error)
	readBuildInfo func() (*debug.BuildInfo, bool)
}

func verifyExecutableProvenance(ctx context.Context, root string, dependencies provenanceDependencies) (inputdigest.State, *provenanceIssue, error) {
	if dependencies.computeState == nil {
		return inputdigest.State{}, nil, errors.New("compute source state dependency is nil")
	}
	if dependencies.readBuildInfo == nil {
		return inputdigest.State{}, nil, errors.New("read build info dependency is nil")
	}
	state, err := dependencies.computeState(ctx, root)
	if err != nil {
		return inputdigest.State{}, nil, err
	}
	buildInfo, available := dependencies.readBuildInfo()
	if !available || buildInfo == nil {
		return state, &provenanceIssue{Code: codeToolRevisionUnavailable, Message: messageToolRevisionUnavailable}, nil
	}
	settings, duplicate := uniqueBuildSettings(buildInfo.Settings)
	if duplicate != "" {
		return state, revisionMismatch("build setting is duplicated"), nil
	}
	vcs, hasVCS := settings["vcs"]
	revision, hasRevision := settings["vcs.revision"]
	modified, hasModified := settings["vcs.modified"]
	if !hasVCS || !hasRevision || !hasModified {
		return state, &provenanceIssue{Code: codeToolRevisionUnavailable, Message: messageToolRevisionUnavailable}, nil
	}
	if vcs != "git" {
		return state, revisionMismatch("executable was not built from Git"), nil
	}
	if !validBuildRevision(revision) {
		return state, revisionMismatch("executable revision is malformed"), nil
	}
	if modified != "false" {
		return state, revisionMismatch("executable was built from modified inputs"), nil
	}
	if revision != state.SourceCommit {
		return state, revisionMismatch("executable revision differs from the current source commit"), nil
	}
	return state, nil, nil
}

func uniqueBuildSettings(source []debug.BuildSetting) (map[string]string, string) {
	settings := make(map[string]string, len(source))
	for _, setting := range source {
		if _, exists := settings[setting.Key]; exists {
			return nil, setting.Key
		}
		settings[setting.Key] = setting.Value
	}
	return settings, ""
}

func validBuildRevision(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}

func revisionMismatch(reason string) *provenanceIssue {
	return &provenanceIssue{
		Code:    codeToolRevisionMismatch,
		Message: fmt.Sprintf("executable build provenance mismatch: %s", reason),
	}
}

func resolveReleaseInput(value string, current inputdigest.Digest) (inputdigest.Digest, *provenanceIssue, error) {
	if _, err := inputdigest.Parse(string(current)); err != nil {
		return "", nil, err
	}
	if value == "current" {
		return current, nil, nil
	}
	requested, err := inputdigest.Parse(value)
	if err != nil {
		return "", nil, err
	}
	if requested != current {
		return "", &provenanceIssue{Code: codeReleaseInputDigestMismatch, Message: messageReleaseInputDigestMismatch}, nil
	}
	return current, nil, nil
}
