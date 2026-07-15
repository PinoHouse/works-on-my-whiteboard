#!/bin/sh
set -eu

task10_verify_error() {
  printf '%s\n' "$1" >&2
}

task10_verify_physical_directory() (
  CDPATH=
  case "$1" in
    /*) cd -P "$1" 2>/dev/null ;;
    *) cd -P "./$1" 2>/dev/null ;;
  esac
  pwd -P
)

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  task10_verify_error "usage: verify-experiments.sh <smoke|deep> <repo-root> [artifact-root]"
  exit 2
fi

task10_verify_profile=$1
task10_verify_repo_root=$2
task10_verify_artifact_root=${3:-}

case "$task10_verify_profile" in
  smoke|deep) ;;
  *)
    task10_verify_error "invalid experiment profile"
    exit 2
    ;;
esac

if [ -z "$task10_verify_repo_root" ] || [ ! -d "$task10_verify_repo_root" ]; then
  task10_verify_error "repository root is unavailable"
  exit 2
fi
if [ "$#" -eq 3 ] && [ -z "$task10_verify_artifact_root" ]; then
  task10_verify_error "artifact root must not be empty"
  exit 2
fi

task10_verify_binary=$task10_verify_repo_root/generated/.bin/whiteboard
if [ ! -x "$task10_verify_binary" ]; then
  task10_verify_error "verified whiteboard executable is unavailable"
  exit 2
fi

task10_verify_tmp_parent=${TMPDIR:-/tmp}
if [ ! -d "$task10_verify_tmp_parent" ]; then
  task10_verify_error "temporary storage is unavailable"
  exit 2
fi
task10_verify_work_root=$(mktemp -d "$task10_verify_tmp_parent/w0-$task10_verify_profile.XXXXXX" 2>/dev/null) || {
  task10_verify_error "temporary evidence storage cannot be created"
  exit 2
}
task10_verify_evidence_root=$task10_verify_work_root/evidence
task10_verify_commands_root=$task10_verify_work_root/commands
if ! mkdir -p "$task10_verify_evidence_root" "$task10_verify_commands_root" 2>/dev/null; then
  rm -rf "$task10_verify_work_root" 2>/dev/null || :
  task10_verify_error "temporary evidence storage cannot be prepared"
  exit 2
fi
task10_verify_repo_physical=$(task10_verify_physical_directory "$task10_verify_repo_root") || {
  rm -rf "$task10_verify_work_root" 2>/dev/null || :
  task10_verify_error "repository root cannot be resolved safely"
  exit 2
}
task10_verify_work_physical=$(task10_verify_physical_directory "$task10_verify_work_root") || {
  rm -rf "$task10_verify_work_root" 2>/dev/null || :
  task10_verify_error "temporary evidence storage cannot be resolved safely"
  exit 2
}
case "$task10_verify_work_physical/" in
  "$task10_verify_repo_physical/"*)
    rm -rf "$task10_verify_work_root" 2>/dev/null || :
    task10_verify_error "temporary evidence storage must be external"
    exit 2
    ;;
esac
task10_verify_child_pid=
task10_verify_current_status_file=

task10_verify_preserve() {
  if [ -z "$task10_verify_artifact_root" ]; then
    return 0
  fi
  if ! mkdir -p "$task10_verify_artifact_root" 2>/dev/null; then
    task10_verify_error "artifact storage cannot be prepared"
    return 1
  fi
  task10_verify_destination=$(mktemp -d "$task10_verify_artifact_root/w0-$task10_verify_profile.XXXXXX" 2>/dev/null) || {
    task10_verify_error "artifact storage cannot be allocated"
    return 1
  }
  if ! cp -R "$task10_verify_work_root/." "$task10_verify_destination/" 2>/dev/null; then
    rm -rf "$task10_verify_destination" 2>/dev/null || :
    task10_verify_destination=
    task10_verify_error "experiment artifacts cannot be preserved"
    return 1
  fi
  return 0
}

task10_verify_remove_temporary() {
  if [ -n "$task10_verify_work_root" ] && ! rm -rf "$task10_verify_work_root" 2>/dev/null; then
    task10_verify_error "temporary evidence storage cannot be cleaned"
    return 1
  fi
  task10_verify_work_root=
  task10_verify_evidence_root=
  return 0
}

task10_verify_on_exit() {
  task10_verify_status=$?
  trap - 0 1 2 3 15
  task10_verify_exit_signal=
  task10_verify_exit_fallback=$task10_verify_status
  case "$task10_verify_status" in
    129) task10_verify_exit_signal=HUP ;;
    130) task10_verify_exit_signal=INT ;;
    131) task10_verify_exit_signal=QUIT ;;
    143) task10_verify_exit_signal=TERM ;;
  esac
  task10_verify_cleanup_status=0
  task10_verify_preserve || task10_verify_cleanup_status=2
  task10_verify_remove_temporary || task10_verify_cleanup_status=2
  if [ "$task10_verify_status" -eq 0 ] && [ "$task10_verify_cleanup_status" -ne 0 ]; then
    task10_verify_status=$task10_verify_cleanup_status
  fi
  if [ -n "$task10_verify_exit_signal" ]; then
    kill -s "$task10_verify_exit_signal" "$$"
    exit "$task10_verify_exit_fallback"
  fi
  exit "$task10_verify_status"
}

task10_verify_on_signal() {
  task10_verify_signal=$1
  task10_verify_fallback=$2
  trap - 0 1 2 3 15
  task10_verify_signaled_pid=$task10_verify_child_pid
  task10_verify_child_pid=
  if [ -n "$task10_verify_signaled_pid" ]; then
    kill -s "$task10_verify_signal" "$task10_verify_signaled_pid" 2>/dev/null || :
    wait "$task10_verify_signaled_pid" 2>/dev/null || :
  fi
  if [ -n "$task10_verify_current_status_file" ]; then
    printf '%s\n' "$task10_verify_fallback" >"$task10_verify_current_status_file" 2>/dev/null || :
  fi
  task10_verify_preserve || :
  task10_verify_remove_temporary || :
  trap - "$task10_verify_signal"
  kill -s "$task10_verify_signal" "$$"
  exit "$task10_verify_fallback"
}

task10_verify_run_child() {
  task10_verify_command_name=$1
  shift
  task10_verify_command_stdout=$task10_verify_commands_root/$task10_verify_command_name.stdout
  task10_verify_command_stderr=$task10_verify_commands_root/$task10_verify_command_name.stderr
  task10_verify_current_status_file=$task10_verify_commands_root/$task10_verify_command_name.exit-status
  "$@" >"$task10_verify_command_stdout" 2>"$task10_verify_command_stderr" &
  task10_verify_child_pid=$!
  task10_verify_child_status=0
  wait "$task10_verify_child_pid" || task10_verify_child_status=$?
  task10_verify_child_pid=
  printf '%s\n' "$task10_verify_child_status" >"$task10_verify_current_status_file"
  task10_verify_current_status_file=
  return "$task10_verify_child_status"
}

trap 'task10_verify_on_exit' 0
trap 'task10_verify_on_signal HUP 129' 1
trap 'task10_verify_on_signal INT 130' 2
trap 'task10_verify_on_signal QUIT 131' 3
trap 'task10_verify_on_signal TERM 143' 15

task10_verify_run_status=0
task10_verify_run_child run "$task10_verify_binary" run \
  --required \
  --profile "$task10_verify_profile" \
  --root "$task10_verify_repo_root" \
  --evidence-root "$task10_verify_evidence_root" \
  --snapshot || task10_verify_run_status=$?
if [ "$task10_verify_run_status" -ne 0 ]; then
  task10_verify_error "required experiment run failed"
  exit "$task10_verify_run_status"
fi

task10_verify_report_status=0
task10_verify_run_child report "$task10_verify_binary" report \
  --root "$task10_verify_repo_root" \
  --evidence-root "$task10_verify_evidence_root" \
  --release current \
  --profile "$task10_verify_profile" \
  --format json \
  --output "$task10_verify_work_root/report.json" || task10_verify_report_status=$?
if [ "$task10_verify_report_status" -ne 0 ]; then
  task10_verify_error "independent evidence report failed"
  exit "$task10_verify_report_status"
fi

if [ "$task10_verify_profile" = deep ] && [ -n "$task10_verify_artifact_root" ]; then
  task10_verify_diff_status=0
  task10_verify_run_child diff "$task10_verify_binary" diff \
    --root "$task10_verify_repo_root" \
    --left-evidence-root "$task10_verify_repo_root/evidence" \
    --right-evidence-root "$task10_verify_evidence_root" \
    --release current \
    --profile deep \
    --format markdown \
    --output "$task10_verify_work_root/diff.md" || task10_verify_diff_status=$?
  case "$task10_verify_diff_status" in
    0) ;;
    129|130|131|143) exit "$task10_verify_diff_status" ;;
    *) task10_verify_error "informational evidence diff is unavailable" ;;
  esac
fi
