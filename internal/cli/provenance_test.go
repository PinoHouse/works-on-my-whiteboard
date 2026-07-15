package cli

import (
	"context"
	"errors"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
)

const (
	testSourceRevision = "0123456789abcdef0123456789abcdef01234567"
	testInputDigest    = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func TestVerifyExecutableProvenanceOrdersAndRetainsOneSourceState(t *testing.T) {
	t.Parallel()
	events := []string{}
	want := inputdigest.State{InputDigest: testInputDigest, SourceCommit: testSourceRevision}
	dependencies := provenanceDependencies{
		computeState: func(context.Context, string) (inputdigest.State, error) {
			events = append(events, "compute-state")
			return want, nil
		},
		readBuildInfo: func() (*debug.BuildInfo, bool) {
			events = append(events, "read-build-info")
			return matchingBuildInfo(testSourceRevision), true
		},
	}

	got, issue, err := verifyExecutableProvenance(context.Background(), "/must-not-leak", dependencies)
	if err != nil || issue != nil {
		t.Fatalf("verifyExecutableProvenance = %+v, %+v, %v", got, issue, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(events, []string{"compute-state", "read-build-info"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestVerifyExecutableProvenanceFailsClosedForHostileBuildInfo(t *testing.T) {
	t.Parallel()
	base := matchingBuildInfo(testSourceRevision)
	tests := []struct {
		name      string
		available bool
		info      *debug.BuildInfo
		wantCode  string
	}{
		{name: "unavailable", available: false, info: base, wantCode: codeToolRevisionUnavailable},
		{name: "nil", available: true, info: nil, wantCode: codeToolRevisionUnavailable},
		{name: "missing vcs", available: true, info: buildInfoWithout(base, "vcs"), wantCode: codeToolRevisionUnavailable},
		{name: "missing revision", available: true, info: buildInfoWithout(base, "vcs.revision"), wantCode: codeToolRevisionUnavailable},
		{name: "missing modified", available: true, info: buildInfoWithout(base, "vcs.modified"), wantCode: codeToolRevisionUnavailable},
		{name: "duplicate same revision", available: true, info: buildInfoWith(base, "vcs.revision", testSourceRevision), wantCode: codeToolRevisionMismatch},
		{name: "duplicate unrelated setting", available: true, info: buildInfoWith(base, "vcs.time", "2026-07-14T00:00:00Z"), wantCode: codeToolRevisionMismatch},
		{name: "non git", available: true, info: buildInfoReplacing(base, "vcs", "hg"), wantCode: codeToolRevisionMismatch},
		{name: "short revision", available: true, info: buildInfoReplacing(base, "vcs.revision", testSourceRevision[:39]), wantCode: codeToolRevisionMismatch},
		{name: "uppercase revision", available: true, info: buildInfoReplacing(base, "vcs.revision", strings.ToUpper(testSourceRevision)), wantCode: codeToolRevisionMismatch},
		{name: "modified", available: true, info: buildInfoReplacing(base, "vcs.modified", "true"), wantCode: codeToolRevisionMismatch},
		{name: "modified capitalization", available: true, info: buildInfoReplacing(base, "vcs.modified", "False"), wantCode: codeToolRevisionMismatch},
		{name: "stale revision", available: true, info: buildInfoReplacing(base, "vcs.revision", "1123456789abcdef0123456789abcdef01234567"), wantCode: codeToolRevisionMismatch},
		{name: "valid sha256 revision", available: true, info: matchingBuildInfo("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			computeCalls := 0
			buildCalls := 0
			stateRevision := testSourceRevision
			if test.name == "valid sha256 revision" {
				stateRevision = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			}
			dependencies := provenanceDependencies{
				computeState: func(context.Context, string) (inputdigest.State, error) {
					computeCalls++
					return inputdigest.State{InputDigest: testInputDigest, SourceCommit: stateRevision}, nil
				},
				readBuildInfo: func() (*debug.BuildInfo, bool) {
					buildCalls++
					return test.info, test.available
				},
			}
			_, issue, err := verifyExecutableProvenance(context.Background(), "/secret/root", dependencies)
			if err != nil {
				t.Fatalf("operational error = %v", err)
			}
			if test.wantCode == "" {
				if issue != nil {
					t.Fatalf("issue = %+v, want nil", issue)
				}
			} else if issue == nil || issue.Code != test.wantCode {
				t.Fatalf("issue = %+v, want code %q", issue, test.wantCode)
			}
			if issue != nil && strings.Contains(issue.Message, "/secret/root") {
				t.Fatalf("issue leaked root: %+v", issue)
			}
			if computeCalls != 1 || buildCalls != 1 {
				t.Fatalf("compute calls=%d build calls=%d", computeCalls, buildCalls)
			}
		})
	}
}

func TestVerifyExecutableProvenanceDoesNotReadBuildInfoAfterStateFailure(t *testing.T) {
	t.Parallel()
	injected := errors.New("state unavailable")
	buildCalls := 0
	_, issue, err := verifyExecutableProvenance(context.Background(), ".", provenanceDependencies{
		computeState: func(context.Context, string) (inputdigest.State, error) {
			return inputdigest.State{}, injected
		},
		readBuildInfo: func() (*debug.BuildInfo, bool) {
			buildCalls++
			return matchingBuildInfo(testSourceRevision), true
		},
	})
	if !errors.Is(err, injected) || issue != nil || buildCalls != 0 {
		t.Fatalf("issue=%+v err=%v build calls=%d", issue, err, buildCalls)
	}
}

func TestResolveReleaseInputRequiresTheFreshCurrentDigest(t *testing.T) {
	t.Parallel()
	current := inputdigest.Digest(testInputDigest)
	stale := "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		name      string
		value     string
		want      inputdigest.Digest
		wantCode  string
		wantError error
	}{
		{name: "current", value: "current", want: current},
		{name: "explicit current", value: testInputDigest, want: current},
		{name: "explicit stale", value: stale, wantCode: codeReleaseInputDigestMismatch},
		{name: "empty", value: "", wantError: inputdigest.ErrInvalidDigest},
		{name: "uppercase", value: strings.ToUpper(testInputDigest), wantError: inputdigest.ErrInvalidDigest},
		{name: "path shaped", value: "../../evidence", wantError: inputdigest.ErrInvalidDigest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, issue, err := resolveReleaseInput(test.value, current)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("error = %v, want %v", err, test.wantError)
			}
			if test.wantError != nil {
				if issue != nil {
					t.Fatalf("malformed input returned semantic issue %+v", issue)
				}
				return
			}
			if test.wantCode != "" {
				if issue == nil || issue.Code != test.wantCode {
					t.Fatalf("issue = %+v, want code %q", issue, test.wantCode)
				}
				return
			}
			if issue != nil || got != test.want {
				t.Fatalf("got=%q issue=%+v, want %q", got, issue, test.want)
			}
		})
	}
}

func matchingBuildInfo(revision string) *debug.BuildInfo {
	return &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: revision},
		{Key: "vcs.time", Value: "2026-07-14T00:00:00Z"},
		{Key: "vcs.modified", Value: "false"},
	}}
}

func buildInfoWithout(source *debug.BuildInfo, key string) *debug.BuildInfo {
	result := &debug.BuildInfo{}
	for _, setting := range source.Settings {
		if setting.Key != key {
			result.Settings = append(result.Settings, setting)
		}
	}
	return result
}

func buildInfoWith(source *debug.BuildInfo, key, value string) *debug.BuildInfo {
	result := &debug.BuildInfo{Settings: append([]debug.BuildSetting{}, source.Settings...)}
	result.Settings = append(result.Settings, debug.BuildSetting{Key: key, Value: value})
	return result
}

func buildInfoReplacing(source *debug.BuildInfo, key, value string) *debug.BuildInfo {
	result := &debug.BuildInfo{Settings: append([]debug.BuildSetting{}, source.Settings...)}
	for index := range result.Settings {
		if result.Settings[index].Key == key {
			result.Settings[index].Value = value
		}
	}
	return result
}
