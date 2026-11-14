"""Unit tests for offline Chainsaw report conversion and aggregation."""
import json
import sys
import tempfile
import unittest
from pathlib import Path
from xml.etree import ElementTree

sys.path.insert(0, str(Path(__file__).parent))
from report import create_junit, summarize  # noqa: E402


def report(status, error=None):
    operation = {"name": "assert", "status": status}
    if error:
        operation["failure"] = {"error": error}
    return {
        "name": "chainsaw",
        "tests": [{
            "basePath": "policies/example",
            "name": "policy-test",
            "status": status,
            "startTime": "2026-11-14T10:00:00Z",
            "endTime": "2026-11-14T10:00:01Z",
            "steps": [{"operations": [operation]}],
        }],
    }


class E2EReportTests(unittest.TestCase):
    def test_junit_preserves_failure_and_duration(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source = root / "results.json"
            dest = root / "results.xml"
            source.write_text(json.dumps(report("failed", "admission denied <bad>")), encoding="utf-8")
            create_junit(source, dest)
            xml = ElementTree.parse(dest)
            suite = xml.find("testsuite")
            self.assertEqual(suite.attrib["failures"], "1")
            case = suite.find("testcase")
            self.assertEqual(case.attrib["time"], "1.0")
            self.assertIn("<bad>", case.find("failure").text)

    def test_skipped_test_is_not_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "results.json").write_text(json.dumps(report("skipped")), encoding="utf-8")
            create_junit(root / "results.json", root / "results.xml")
            xml = ElementTree.parse(root / "results.xml")
            self.assertEqual(xml.find("testsuite").attrib["skipped"], "1")
            self.assertIsNotNone(xml.find(".//skipped"))

    def write_artifact(self, root, version, status, error=None):
        directory = root / ("report-" + version)
        directory.mkdir()
        (directory / "context.json").write_text(json.dumps({
            "suite": "generating-policies",
            "k8s": version,
            "run_url": "https://github.com/kyverno/kyverno/actions/runs/123",
        }), encoding="utf-8")
        (directory / "results.json").write_text(json.dumps(report(status, error)), encoding="utf-8")

    def test_some_versions_flags_possible_flake(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.write_artifact(root, "v1.35.0", "failed", "expected | actual")
            self.write_artifact(root, "v1.36.0", "passed")
            markdown = summarize(root)
            self.assertIn("Some versions (possibly flaky)", markdown)
            self.assertIn("v1.35.0", markdown)
            self.assertIn("expected \\| actual", markdown)
            self.assertIn("[Run logs]", markdown)

    def test_all_versions_and_missing_reports(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.write_artifact(root, "v1.35.0", "failed", "timeout")
            self.write_artifact(root, "v1.36.0", "failed", "timeout")
            empty = root / "report-v1.34.0"
            empty.mkdir()
            (empty / "context.json").write_text(json.dumps({
                "suite": "generating-policies", "k8s": "v1.34.0",
            }), encoding="utf-8")
            markdown = summarize(root)
            self.assertIn("Every observed version", markdown)
            self.assertIn("Incomplete evidence", markdown)

    def test_no_reports_is_not_misreported_as_green(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.assertIn("No Chainsaw JSON reports were available", summarize(tmp))


if __name__ == "__main__":
    unittest.main()
