# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
"""Exercise publication checks with real Git commits and pre-push input."""

import hashlib
import json
import shlex
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import verify_commit as gate


AUTHOR = "renovate-example[bot] <123+renovate-example[bot]@users.noreply.github.com>"
RECORDING = "test/e2e/recorded_data/example/v0.2.0/example-org-workload-v1/running.yaml"
EXISTING = "test/e2e/recorded_data/example/v0.1.0/example-org-workload-v1/running.yaml"
BEFORE = b'EXAMPLE_VERSION="${EXAMPLE_VERSION:-v0.1.0}"\n'
AFTER = b'EXAMPLE_VERSION="${EXAMPLE_VERSION:-v0.2.0}"\n'
RECORDING_BYTES = b"schemaVersion: 1\nresult:\n  succeeded: true\n"


class VerifyCommitTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.manifest = self.root / "manifest.json"
        self.git("init", "--quiet")
        self.git("config", "user.name", "renovate-example[bot]")
        self.git("config", "user.email", "123+renovate-example[bot]@users.noreply.github.com")
        self.write(gate.ENV_PATH, BEFORE)
        self.write(EXISTING, RECORDING_BYTES)
        self.commit()
        self.base = self.git("rev-parse", "HEAD").strip()
        self.files = {gate.ENV_PATH: AFTER, RECORDING: RECORDING_BYTES}
        self.write_manifest()

    def git(self, *args):
        return subprocess.check_output(
            ["git", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", *args],
            cwd=self.repo, stderr=subprocess.PIPE, text=True,
        )

    def write(self, path, data):
        dest = self.repo / path
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_bytes(data)

    def write_manifest(self):
        self.manifest.write_text(json.dumps({
            "base": self.base,
            "group": "example",
            "files": {path: hashlib.sha256(data).hexdigest() for path, data in self.files.items()},
        }))

    def commit(self, signoff=True, amend=False):
        self.git("add", "--all")
        args = ["commit", "--quiet", "-m", "build(e2e): update example"]
        if signoff:
            args.append("--signoff")
        if amend:
            args.append("--amend")
        self.git(*args)
        return self.git("rev-parse", "HEAD").strip()

    def candidate(self, signoff=True):
        for path, data in self.files.items():
            self.write(path, data)
        return self.commit(signoff=signoff)

    def verify(self, **kwargs):
        return gate.verify(self.manifest, self.base, AUTHOR, self.repo, **kwargs)

    def push(self, sha, branch="refs/heads/renovate/e2e-example"):
        return f"refs/heads/candidate {sha} {branch} {'0' * 40}\n"

    def test_accepts_tested_bot_commit(self):
        self.candidate()
        self.verify()

    def test_checks_committed_bytes_not_dirty_worktree(self):
        self.candidate()
        self.write(RECORDING, b"uncommitted local content")
        self.verify()

    def test_accepts_unchanged_recording_but_still_checks_its_hash(self):
        self.files = {gate.ENV_PATH: AFTER, EXISTING: RECORDING_BYTES}
        self.write_manifest()
        self.candidate()
        self.verify()
        self.files[EXISTING] = b"different tested bytes"
        self.write_manifest()
        with self.assertRaisesRegex(ValueError, "differs from the tested content"):
            self.verify()

    def test_missing_manifest_blocks_publication(self):
        self.candidate()
        self.manifest.unlink()
        with self.assertRaisesRegex(ValueError, "regular file"):
            self.verify()

    def test_manifest_must_be_outside_checkout(self):
        self.candidate()
        inside = self.repo / "manifest.json"
        inside.write_bytes(self.manifest.read_bytes())
        with self.assertRaisesRegex(ValueError, "outside"):
            gate.verify(inside, self.base, AUTHOR, self.repo)

    def test_symlink_manifest_is_rejected(self):
        self.candidate()
        link = self.root / "link.json"
        link.symlink_to(self.manifest)
        with self.assertRaisesRegex(ValueError, "regular file"):
            gate.verify(link, self.base, AUTHOR, self.repo)

    def test_duplicate_manifest_entries_are_rejected(self):
        self.candidate()
        data = self.manifest.read_text()
        self.manifest.write_text(data[:-1] + ', "group": "example"}')
        with self.assertRaisesRegex(ValueError, "duplicate"):
            self.verify()

    def test_stale_manifest_base_is_rejected(self):
        self.candidate()
        data = json.loads(self.manifest.read_text())
        data["base"] = "a" * 40
        self.manifest.write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, "expected base"):
            self.verify()

    def test_extra_commit_after_verified_base_is_rejected(self):
        self.candidate()
        self.write(RECORDING, b"another commit")
        self.commit()
        with self.assertRaisesRegex(ValueError, "exactly one parent"):
            self.verify()

    def test_wrong_author_is_rejected(self):
        self.candidate()
        self.git("commit", "--amend", "--no-edit", "--author", "Someone Else <other@example.com>")
        with self.assertRaisesRegex(ValueError, "authored"):
            self.verify()

    def test_missing_dco_is_rejected(self):
        self.candidate(signoff=False)
        with self.assertRaisesRegex(ValueError, "DCO"):
            self.verify()

    def test_different_recording_content_is_rejected(self):
        self.candidate()
        self.write(RECORDING, b"not the tested recording")
        self.commit(amend=True)
        with self.assertRaisesRegex(ValueError, "differs from the tested content"):
            self.verify()

    def test_unrecorded_extra_change_is_rejected(self):
        self.write("README.md", b"unexpected change")
        self.candidate()
        with self.assertRaisesRegex(ValueError, "absent from the CI manifest"):
            self.verify()

    def test_removal_is_rejected(self):
        (self.repo / EXISTING).unlink()
        self.candidate()
        with self.assertRaisesRegex(ValueError, "only add or modify"):
            self.verify()

    def test_missing_recording_is_rejected(self):
        self.write(gate.ENV_PATH, AFTER)
        self.commit()
        with self.assertRaisesRegex(ValueError, "all tested files"):
            self.verify()

    def test_unchanged_pin_is_rejected(self):
        self.files[gate.ENV_PATH] = BEFORE
        self.write_manifest()
        self.candidate()
        with self.assertRaisesRegex(ValueError, "modify global.env"):
            self.verify()

    def test_executable_recording_is_rejected(self):
        self.candidate()
        (self.repo / RECORDING).chmod(0o755)
        self.commit(amend=True)
        with self.assertRaisesRegex(ValueError, "regular, non-executable"):
            self.verify()

    def test_symlink_recording_is_rejected(self):
        self.write(gate.ENV_PATH, AFTER)
        path = self.repo / RECORDING
        path.parent.mkdir(parents=True)
        path.symlink_to(self.repo / EXISTING)
        self.commit()
        with self.assertRaisesRegex(ValueError, "regular, non-executable"):
            self.verify()

    def test_manifest_cannot_authorize_other_paths(self):
        for path in ("README.md", "test/e2e/recorded_data/x/../x/running.yaml"):
            with self.subTest(path=path):
                self.files[path] = b"unexpected"
                self.write_manifest()
                with self.assertRaisesRegex(ValueError, "unexpected path|unsafe path"):
                    self.verify()
                del self.files[path]

    def test_accepts_composite_versions_in_recording_path(self):
        self.files = {
            gate.ENV_PATH: AFTER,
            RECORDING.replace("v0.2.0", "v1.9.0+mpiv0.8.2"): RECORDING_BYTES,
        }
        self.write_manifest()
        self.candidate()
        self.verify()

    def test_real_push_requires_manifest_before_creating_remote_branch(self):
        candidate = self.candidate()
        remote = self.root / "remote.git"
        self.git("init", "--quiet", "--bare", str(remote))
        hooks = self.root / "hooks"
        hooks.mkdir()
        command = [
            sys.executable, str(Path(gate.__file__).resolve()),
            "--manifest", str(self.manifest), "--base-sha", self.base,
            "--author", AUTHOR, "--pre-push", "--branch", "renovate/e2e-example",
        ]
        hook = hooks / "pre-push"
        hook.write_text("#!/bin/sh\nexec " + shlex.join(command) + "\n")
        hook.chmod(0o755)
        push = [
            "git", "-c", f"core.hooksPath={hooks}", "push", "--quiet", str(remote),
            "HEAD:refs/heads/renovate/e2e-example",
        ]
        self.manifest.unlink()
        blocked = subprocess.run(push, cwd=self.repo, capture_output=True, text=True)
        self.assertNotEqual(blocked.returncode, 0)
        self.assertIn("manifest must be an absolute path to a regular file", blocked.stderr)
        self.assertEqual(self.git("--git-dir", str(remote), "for-each-ref", "--format=%(refname)"), "")

        self.write_manifest()
        allowed = subprocess.run(push, cwd=self.repo, capture_output=True, text=True)
        self.assertEqual(allowed.returncode, 0, allowed.stdout + allowed.stderr)
        self.assertEqual(
            self.git("--git-dir", str(remote), "rev-parse", "refs/heads/renovate/e2e-example").strip(),
            candidate,
        )

    def test_push_checks_actual_object_instead_of_head(self):
        candidate = self.candidate()
        self.write("README.md", b"unrelated HEAD")
        unrelated = self.commit()
        gate.verify_push(self.manifest, self.base, AUTHOR, self.repo, self.push(candidate))
        with self.assertRaisesRegex(ValueError, "exactly one parent"):
            gate.verify_push(self.manifest, self.base, AUTHOR, self.repo, self.push(unrelated))

    def test_push_rejects_other_remote_ref(self):
        candidate = self.candidate()
        for branch in ("refs/heads/main", "refs/tags/test", "refs/heads/renovate/e2e-other"):
            with self.subTest(branch=branch), self.assertRaisesRegex(ValueError, "configured operator"):
                gate.verify_push(self.manifest, self.base, AUTHOR, self.repo, self.push(candidate, branch))

    def test_push_accepts_explicit_expected_branch(self):
        candidate = self.candidate()
        branch = "renovate/e2e-example-validation"
        gate.verify_push(
            self.manifest, self.base, AUTHOR, self.repo,
            self.push(candidate, "refs/heads/" + branch), branch,
        )

    def test_push_rejects_deletion(self):
        self.candidate()
        with self.assertRaisesRegex(ValueError, "not delete"):
            gate.verify_push(self.manifest, self.base, AUTHOR, self.repo, self.push("0" * 40))

    def test_push_rejects_missing_or_multiple_ref_updates(self):
        candidate = self.candidate()
        for updates in ("", self.push(candidate) * 2, "invalid\n"):
            with self.subTest(updates=updates), self.assertRaisesRegex(ValueError, "exactly one branch"):
                gate.verify_push(self.manifest, self.base, AUTHOR, self.repo, updates)


if __name__ == "__main__":
    unittest.main()
