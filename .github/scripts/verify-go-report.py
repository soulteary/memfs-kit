"""Publish only reports produced by one complete Go Report Card invocation."""

# Adapted from health-kit at 4c8be735ad9da47b2a46c3458071911578f51fb2:
# https://github.com/soulteary/health-kit/blob/4c8be735ad9da47b2a46c3458071911578f51fb2/.github/scripts/verify-go-report.py
import json
import pathlib
import subprocess
import sys
import tempfile


OUTPUT = pathlib.Path(".github")
RAW = OUTPUT / "test-results"
REPORTS = (OUTPUT / "goreportcard.svg", OUTPUT / "goreportcard-report.md")
RAW_JSON = RAW / "goreportcard.json"
REQUIRED = {"gofmt", "go_vet", "ineffassign", "gocyclo", "license", "misspell"}


def check_directory(path):
    if path.is_symlink() or (path.exists() and not path.is_dir()):
        raise ValueError(f"Refusing non-directory or symlink report directory: {path}")
    path.mkdir(exist_ok=True)


def check_file(path):
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise ValueError(f"Refusing non-file or symlink report target: {path}")


def remove_unverified_reports():
    # Never follow an unsafe parent or delete the target of a symlink.
    if OUTPUT.is_symlink() or not OUTPUT.is_dir():
        return
    for path in REPORTS:
        if path.is_symlink() or path.is_file():
            path.unlink()


def main():
    check_directory(OUTPUT)
    check_directory(RAW)
    for path in (*REPORTS, RAW_JSON):
        check_file(path)
    # The action has already written reports, but they have not been verified.
    remove_unverified_reports()
    RAW_JSON.unlink(missing_ok=True)

    with tempfile.TemporaryDirectory() as temporary:
        temporary = pathlib.Path(temporary)
        result = subprocess.run(
            ["goreportcard-cli", "-d", ".", "-j",
             "-svg", str(temporary / "goreportcard.svg"),
             "-report", str(temporary / "goreportcard-report.md")],
            capture_output=True, text=True, check=False,
        )
        if result.stderr:
            print(result.stderr, file=sys.stderr, end="")
        # File-write confirmations precede the final JSON line.
        lines = result.stdout.splitlines()
        report = None
        if lines:
            try:
                report = json.loads(lines[-1])
            except json.JSONDecodeError:
                pass
            else:
                # Preserve this invocation's evidence even when a checker failed.
                RAW_JSON.write_text(lines[-1] + "\n", encoding="utf-8")
        result.check_returncode()
        if not isinstance(report, dict):
            raise ValueError("Go Report Card did not return a JSON report")
        checks = report.get("checks")
        if (not isinstance(checks, list) or len(checks) != len(REQUIRED)
                or any(not isinstance(check, dict) for check in checks)
                or {check.get("name") for check in checks} != REQUIRED):
            raise ValueError("Go Report Card must return exactly the six required checkers")
        if report.get("did_error") is not False:
            raise ValueError("Go Report Card reports an execution error")
        for check in checks:
            if check.get("error") != "":
                raise ValueError(f"Go Report Card checker {check['name']} failed: {check.get('error')}")

        files = [(target, (temporary / target.name).read_bytes()) for target in REPORTS]
        if any(not data for _, data in files):
            raise ValueError("Go Report Card produced an empty report file")
        for target, data in files:
            target.write_bytes(data)
    print("Verified all six Go Report Card checkers and their generated reports.")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, TypeError, subprocess.SubprocessError) as error:
        remove_unverified_reports()
        print(f"Refusing to publish incomplete Go Report Card evidence: {error}", file=sys.stderr)
        sys.exit(1)
