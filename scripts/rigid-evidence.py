#!/usr/bin/env python3
"""Build native byte witnesses and collect an unsigned rigid-prop evidence packet."""

import argparse
import difflib
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
BASELINE = ROOT / "testdata/compiler/approved/examples-goalpost.provisional.sha256"
REFERENCES = ROOT / "examples/goalpost/ref/references.sha256"
REFERENCES_V1_SHA256 = "4dc5de8c22520c7eaa971b2de7e665a23a42ef13ad7ddb5c0f1f5a79b101226f"
FIXTURES = {
    "goalpost": (
        "goalpost-lod1.glb", "goalpost-lod2.glb", "goalpost-practice-lod1.glb",
        "goalpost-practice-lod2.glb", "goalpost-practice.glb", "goalpost.glb",
    ),
    "helmet": ("helmet-aero-lod1.glb", "helmet-aero.glb", "helmet-classic-lod1.glb", "helmet-classic.glb"),
    "arm": ("arm.glb",),
}
VALIDATOR_VERSION = "2.0.0-dev.3.10"
VALIDATOR_LINUX_AMD64_SHA256 = "5cba1e097935c9efd929d24a49610824259b030f0ae45672273bfbcbb5b87a62"
CONTROLS = (
    "TestControlCapRemoval",
    "TestControlShiftedSweepDetaches",
    "TestMovedUprightIsCaught",
    "TestApprovedGoalPostOutlineRejectsMovedUprights",
    "TestStalePinRefusesTheBuild",
    "TestDecodedRingCorrespondenceControl",
    "TestOutlineLockedCamera",
    "TestOutlineIsUnavailable",
)


def run(args, *, cwd=ROOT, env=None, output=None, expected=(0,)):
    if output is None:
        code = subprocess.run(args, cwd=cwd, env=env).returncode
    else:
        with output.open("wb") as report:
            code = subprocess.run(args, cwd=cwd, env=env, stdout=report).returncode
    if code not in expected:
        raise ValueError(f"{' '.join(map(str, args))} exited {code}, expected {expected}")


def read_manifest(path):
    rows = {}
    for row in path.read_text().splitlines():
        if row.startswith("#"):
            continue
        fields = row.split()
        if (len(fields) != 3 or not re.fullmatch("[0-9a-f]{64}", fields[0])
                or not re.fullmatch("[1-9][0-9]*", fields[1]) or fields[2] in rows):
            raise ValueError(f"{path}: invalid or duplicate manifest row {row!r}")
        digest, size, name = fields
        rows[name] = (digest, int(size))
    return rows


def verify_manifest(path):
    rows = read_manifest(path)
    expected = {
        f"{fixture}/{name}"
        for fixture, names in FIXTURES.items()
        for name in (*names, f"{fixture}.build.json")
    }
    if rows.keys() != expected:
        raise ValueError(f"{path}: missing {sorted(expected - rows.keys())}, unexpected {sorted(rows.keys() - expected)}")
    baseline = read_manifest(BASELINE)
    if baseline.keys() != set(FIXTURES["goalpost"]):
        raise ValueError(f"{BASELINE}: unexpected goal post inventory")
    for name, value in baseline.items():
        if rows[f"goalpost/{name}"] != value:
            raise ValueError(f"{path}: {name} differs from the provisional regression baseline")
    return rows


def collect(out, greenware):
    entries = []
    for fixture, names in FIXTURES.items():
        directory = out / fixture
        source = ROOT / f"examples/{fixture}/{fixture}.gw"
        run([greenware, "build", source, "--out-dir", directory, "--json"],
            output=out / f"{fixture}.build-report.json")
        expected = {*names, f"{fixture}.build.json"}
        actual = {p.name for p in directory.iterdir()}
        if actual != expected:
            raise ValueError(f"{fixture}: missing {sorted(expected - actual)}, unexpected {sorted(actual - expected)}")
        for name in sorted(expected):
            content = (directory / name).read_bytes()
            entries.append((f"{fixture}/{name}", hashlib.sha256(content).hexdigest(), len(content)))
    manifest = out / "manifest.sha256"
    manifest.write_text("".join(f"{digest} {size} {name}\n" for name, digest, size in sorted(entries)),
                        encoding="ascii", newline="\n")
    verify_manifest(manifest)
    return manifest


def compare(left, right):
    a = verify_manifest(left)
    b = verify_manifest(right)
    if a != b:
        differences = "\n".join(difflib.unified_diff(
            left.read_text().splitlines(), right.read_text().splitlines(),
            fromfile=str(left), tofile=str(right), lineterm="",
        ))
        raise ValueError(f"native build bytes differ\n{differences}")
    print(f"Matched SHA-256 and length for all {len(a)} files")


def verify_references(levels):
    manifest = REFERENCES.read_bytes()
    if hashlib.sha256(manifest).hexdigest() != REFERENCES_V1_SHA256:
        raise ValueError("goal post reference manifest differs from approved v1")
    rows = dict((name, digest) for digest, name in (
        row.split() for row in manifest.decode("ascii").splitlines()
    ))
    expected = {
        f"ref/goalpost-{output}-lod{lod}.png{suffix}"
        for output, lod in levels
        for suffix in ("", ".view.json")
    }
    if rows.keys() != expected:
        raise ValueError("unexpected goal post reference inventory")
    for name, digest in rows.items():
        if hashlib.sha256((REFERENCES.parent.parent / name).read_bytes()).hexdigest() != digest:
            raise ValueError(f"{name} differs from approved v1")
    return rows


def check_acceptance(report_path):
    report = json.loads(report_path.read_bytes())
    if report["schema"] != "greenware.accept/1":
        raise ValueError("unknown acceptance report schema")
    levels = report["levels"]
    expected = {(output, lod) for output in ("stadium", "practice") for lod in range(3)}
    actual = {(level["output"], level["lod"]) for level in levels}
    if len(levels) != len(expected) or actual != expected:
        raise ValueError("unexpected acceptance output or level inventory")
    references = verify_references(expected)
    for level in levels:
        if level.get("problem"):
            raise ValueError(level["problem"])
        name = f"ref/goalpost-{level['output']}-lod{level['lod']}.png"
        expects = level["expects"]
        outlines = [e for e in expects if e["message"] == "front outline"]
        count = 11 if level["output"] == "stadium" else 9
        if len(expects) != count or len(outlines) != 1:
            raise ValueError(f"{name}: expected {count} checks including one outline")
        for expect in expects:
            if expect["verdict"] != "pass":
                raise ValueError(f"{name}: {expect['message']!r} was {expect['verdict']}")
        measurements = {m["name"]: m["value"] for m in outlines[0]["measures"]}
        if (measurements.get("outline mask sha256") != references[name]
                or measurements.get("outline camera sha256") != references[name + ".view.json"]):
            raise ValueError(f"{name}: acceptance did not use the approved mask and camera")
    totals = report["totals"]
    if totals["pass"] != 60 or any(
        totals.get(key, 0) != 0 for key in ("fail", "refused", "missing", "unavailable")
    ):
        raise ValueError(f"unexpected acceptance totals: {totals}")
    return totals


def validate_khronos(out, validator, expected_digest):
    digest = hashlib.sha256(validator.read_bytes()).hexdigest()
    if digest != expected_digest:
        raise ValueError(f"Khronos validator SHA-256 {digest} differs from the pin {expected_digest}")
    warnings = 0
    for name in FIXTURES["goalpost"]:
        path = out / f"{name}.khronos.json"
        run([validator, "--stdout", "--no-write-timestamp", "--no-absolute-path", name],
            cwd=out / "goalpost", output=path)
        report = json.loads(path.read_bytes())
        if report["validatorVersion"] != VALIDATOR_VERSION or report["issues"]["numErrors"] != 0:
            raise ValueError(f"unexpected Khronos report: {path}")
        warnings += report["issues"]["numWarnings"]
    return {"version": VALIDATOR_VERSION, "sha256": digest, "licence": "Apache-2.0",
            "files_checked": len(FIXTURES["goalpost"]), "errors": 0, "warnings": warnings}


def qualify(out, validator, validator_digest):
    env = {**os.environ, "CGO_ENABLED": "0", "GOWORK": str(ROOT / "go.work")}
    with tempfile.TemporaryDirectory(prefix="greenware-rigid-") as scratch:
        suffix = ".exe" if os.name == "nt" else ""
        greenware = Path(scratch) / f"greenware{suffix}"
        oracle = Path(scratch) / f"oracle{suffix}"
        run(["go", "build", "-mod=readonly", "-o", greenware, "./cmd/greenware"], env=env)
        run(["go", "build", "-mod=readonly", "-o", oracle, "./cmd/oracle"],
            cwd=ROOT / "conformance", env={**env, "GOWORK": "off"})
        run([greenware, "version", "--json"], output=out / "version.json")
        collect(out, greenware)
        acceptance = out / "goalpost.acceptance.json"
        run([greenware, "accept", ROOT / "examples/goalpost/goalpost.gw",
             ROOT / "examples/goalpost/goalpost.accept.gw", "--glb-dir", out / "goalpost", "--json"],
            output=acceptance)
        totals = check_acceptance(acceptance)
        for name in FIXTURES["goalpost"]:
            run([oracle, "check", out / "goalpost" / name], output=out / f"{name}.oracle.json")
        khronos = validate_khronos(out, validator, validator_digest) if validator else None
        controls = out / "controls.jsonl"
        run(["go", "test", "-mod=readonly", "-short", "-count=1", "-json", "./compiler", "./accept",
             "-run", "^(" + "|".join(CONTROLS) + ")$"], env=env, output=controls)
        passed = {event.get("Test") for event in map(json.loads, controls.read_text().splitlines())
                  if event["Action"] == "pass"}
        if not set(CONTROLS) <= passed:
            raise ValueError(f"missing passing controls: {sorted(set(CONTROLS) - passed)}")
    pending = ["native peer comparison", "stock-viewer capture", "intent-only prop trial",
               "published CLI reproduction", "owner sign-off"]
    if khronos is None:
        pending.append("Khronos validator")
    summary = {
        "schema": "greenware.rigid-evidence/1",
        "scope": "goalpost geometry",
        "status": "geometry evidence collected",
        "acceptance": totals,
        "reference_manifest_sha256": REFERENCES_V1_SHA256,
        "oracle_files_checked": len(FIXTURES["goalpost"]),
        "controls_passed": list(CONTROLS),
        "khronos": khronos,
        "pending": pending,
    }
    (out / "summary.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    witness = commands.add_parser("witness")
    witness.add_argument("out", type=Path)
    witness.add_argument("--greenware", type=Path, required=True)
    comparison = commands.add_parser("compare")
    comparison.add_argument("left", type=Path)
    comparison.add_argument("right", type=Path)
    qualification = commands.add_parser("qualify")
    qualification.add_argument("out", type=Path)
    qualification.add_argument("--validator", type=Path, help="local Khronos glTF validator executable")
    qualification.add_argument("--validator-sha256", default=VALIDATOR_LINUX_AMD64_SHA256,
                               help="expected executable digest, defaults to the release's Linux amd64 binary")
    args = parser.parse_args()
    try:
        if args.command == "compare":
            compare(args.left, args.right)
        else:
            out = args.out.resolve()
            out.mkdir(parents=True)
            if args.command == "witness":
                collect(out, args.greenware.resolve())
            else:
                qualify(out, args.validator.resolve() if args.validator else None, args.validator_sha256)
            print(f"Evidence written to {out}")
    except (OSError, ValueError, KeyError) as exc:
        print(f"rigid evidence: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
