#!/usr/bin/env python3
"""Convert Chainsaw JSON reports to JUnit and summarize E2E failures.

Chainsaw supports one --report-format per run. Keep its native JSON as the
source of truth and derive JUnit without running the tests a second time.
"""
import argparse
import json
from collections import defaultdict
from datetime import datetime
from pathlib import Path
import xml.etree.ElementTree as ET


def load_json(path):
    with open(path, encoding="utf-8") as stream:
        return json.load(stream)


def first_error(test):
    for step in test.get("steps", []):
        for operation in step.get("operations", []):
            failure = operation.get("failure") or {}
            if failure.get("error"):
                return str(failure["error"])
    return "Chainsaw reported a failed test; see the job log."


def duration(start, end):
    try:
        a = datetime.fromisoformat(start.replace("Z", "+00:00"))
        b = datetime.fromisoformat(end.replace("Z", "+00:00"))
        return str(max(0.0, (b - a).total_seconds()))
    except (ValueError, AttributeError, TypeError):
        return "0"


def xml_text(value):
    """Remove control characters forbidden by XML 1.0."""
    return "".join(
        char for char in str(value)
        if char in "\t\n\r" or "\u0020" <= char <= "\ud7ff"
        or "\ue000" <= char <= "\ufffd"
    )


def create_junit(source, destination):
    report = load_json(source)
    tests = report.get("tests", [])
    root = ET.Element("testsuites", name=xml_text(report.get("name", "chainsaw")))
    by_path = defaultdict(list)
    for test in tests:
        by_path[str(test.get("basePath", "chainsaw"))].append(test)

    for base_path, suite_tests in sorted(by_path.items()):
        failures = sum(t.get("status") == "failed" for t in suite_tests)
        skips = sum(t.get("status") == "skipped" for t in suite_tests)
        suite = ET.SubElement(root, "testsuite", {
            "name": xml_text(base_path),
            "tests": str(len(suite_tests)),
            "failures": str(failures),
            "skipped": str(skips),
            "errors": "0",
        })
        for test in suite_tests:
            case = ET.SubElement(suite, "testcase", {
                "name": xml_text(test.get("name", "unknown")),
                "classname": xml_text(base_path),
                "time": duration(test.get("startTime"), test.get("endTime")),
            })
            if test.get("status") == "skipped":
                ET.SubElement(case, "skipped")
            elif test.get("status") == "failed":
                error = xml_text(first_error(test))
                ET.SubElement(case, "failure", message=error[:400]).text = error

    destination = Path(destination)
    destination.parent.mkdir(parents=True, exist_ok=True)
    ET.indent(root)
    ET.ElementTree(root).write(destination, encoding="utf-8", xml_declaration=True)


def sanitize_cell(value, limit=220):
    value = " ".join(str(value).split())
    value = value.replace("|", r"\|").replace("`", "'")
    return value[:limit] + ("…" if len(value) > limit else "")


def summarize(folder):
    """Build one row per failed suite/test across the observed K8s versions."""
    observed = defaultdict(set)
    failures = defaultdict(dict)
    missing = []
    report_count = 0

    for metadata_path in sorted(Path(folder).rglob("context.json")):
        context = load_json(metadata_path)
        json_path = metadata_path.with_name("results.json")
        if not json_path.is_file():
            missing.append(f"{context.get('suite', '?')} / {context.get('k8s', '?')}")
            continue
        try:
            report = load_json(json_path)
        except (OSError, ValueError) as error:
            missing.append(f"{metadata_path.parent.name}: {error}")
            continue
        report_count += 1
        suite = str(context.get("suite", "unknown"))
        k8s = str(context.get("k8s", "unknown"))
        link = str(context.get("run_url", ""))
        for test in report.get("tests", []):
            name = str(test.get("basePath", "")).strip("/") + "/" + str(test.get("name", "unknown"))
            key = (suite, name)
            if test.get("status") != "skipped":
                observed[key].add(k8s)
            if test.get("status") == "failed":
                failures[key][k8s] = (first_error(test), link)

    lines = ["### Chainsaw failure summary", ""]
    if not failures:
        if report_count:
            lines.append(f"No failing Chainsaw test cases found in {report_count} JSON reports.")
        else:
            lines.append("No Chainsaw JSON reports were available. Check the failing setup/build job logs.")
    else:
        lines.extend([
            "| Suite / test | Failing Kubernetes versions | First error | Logs | Scope |",
            "| --- | --- | --- | --- | --- |",
        ])
        for (suite, test), versions in sorted(failures.items())[:70]:
            tested = observed[(suite, test)]
            failed = set(versions)
            if len(tested) <= 1:
                scope = "One version observed"
            elif failed == tested:
                scope = "Every observed version"
            else:
                scope = "Some versions (possibly flaky)"
            first = next(iter(versions.values()))
            links = sorted({link for _, link in versions.values() if link.startswith("https://github.com/")})
            log_link = f"[Run logs]({links[0]})" if links else "See Actions run"
            lines.append(
                f"| {sanitize_cell(suite + ' / ' + test, 130)} "
                f"| {sanitize_cell(', '.join(sorted(failed)), 100)} "
                f"| {sanitize_cell(first[0])} | {log_link} | {scope} |"
            )
        if len(failures) > 70:
            lines.append(f"\nOnly the first 70 of {len(failures)} failing tests are shown.")
    if missing:
        lines.append(
            "\n**Incomplete evidence:** Missing or unreadable reports for: "
            + sanitize_cell(", ".join(missing), 600)
            + ". Version comparisons only cover available reports."
        )
    lines.append("")
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    junit = commands.add_parser("junit")
    junit.add_argument("source")
    junit.add_argument("destination")
    meta = commands.add_parser("metadata")
    meta.add_argument("--suite", required=True)
    meta.add_argument("--k8s", required=True)
    meta.add_argument("--run-url", required=True)
    meta.add_argument("--output", required=True)
    summary = commands.add_parser("summary")
    summary.add_argument("--input-dir", required=True)
    summary.add_argument("--output", required=True)
    args = parser.parse_args()

    if args.command == "junit":
        create_junit(args.source, args.destination)
    elif args.command == "metadata":
        Path(args.output).parent.mkdir(parents=True, exist_ok=True)
        Path(args.output).write_text(json.dumps({
            "suite": args.suite, "k8s": args.k8s, "run_url": args.run_url,
        }), encoding="utf-8")
    elif args.command == "summary":
        Path(args.output).parent.mkdir(parents=True, exist_ok=True)
        Path(args.output).write_text(summarize(args.input_dir), encoding="utf-8")


if __name__ == "__main__":
    main()
