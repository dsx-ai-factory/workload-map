# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

import candidate


class CandidateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'repo'
        self.root.mkdir()
        previous = Path.cwd()
        os.chdir(self.root)
        self.addCleanup(os.chdir, previous)
        self.call('git', 'init', '-q')
        self.call('git', 'config', 'user.name', 'Test')
        self.call('git', 'config', 'user.email', 'test@example.com')
        self.env = self.root / candidate.ENV_PATH
        self.env.parent.mkdir(parents=True)
        self.env.write_text('JOBSET_VERSION="${JOBSET_VERSION:-v0.10.1}"\nLWS_VERSION="${LWS_VERSION:-v0.7.0}"\n')
        self.call('git', 'add', '.')
        self.call('git', '-c', 'commit.gpgsign=false', 'commit', '-qm', 'base')
        self.base = self.call('git', 'rev-parse', 'HEAD').strip()
        self.plan = {'group': 'jobset', 'pins': [{'variable': 'JOBSET_VERSION'}],
                     'workloads': ['jobset'], 'webhookMode': 'auto',
                     'recordings': [{'operator': 'jobset', 'versionVariables': ['JOBSET_VERSION']}]}
        self.env.write_text(self.env.read_text().replace('v0.10.1', 'v0.12.0'))

    def call(self, *args):
        return subprocess.check_output(args, text=True, stderr=subprocess.PIPE)

    def prepare(self):
        return candidate.prepare(self.plan, self.base, self.root)

    def test_candidate_binds_pin_and_recording_version(self):
        result = self.prepare()
        self.assertEqual(result['versions'], {'jobset': 'v0.12.0'})
        self.assertEqual(result['pins'], {'JOBSET_VERSION': {'before': 'v0.10.1', 'after': 'v0.12.0'}})

    def test_other_pin_cannot_change(self):
        self.env.write_text(self.env.read_text().replace('v0.7.0', 'v0.8.0'))
        with self.assertRaisesRegex(ValueError, 'only.*group'):
            self.prepare()

    def test_non_pin_edit_cannot_hide_in_env(self):
        self.env.write_text(self.env.read_text() + '# unexpected\n')
        with self.assertRaisesRegex(ValueError, 'other than'):
            self.prepare()

    def test_untracked_source_is_rejected(self):
        Path('unexpected.go').write_text('package main')
        with self.assertRaisesRegex(ValueError, 'untracked'):
            self.prepare()

    def test_stale_base_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'base commit'):
            candidate.prepare(self.plan, '0' * 40, self.root)

    def test_installed_version_cannot_fall_back_to_kubernetes(self):
        result = self.prepare()
        with tempfile.NamedTemporaryFile(mode='w', delete=False) as output:
            output.write('jobset=v1.34.0\n')
            path = Path(output.name)
        self.addCleanup(path.unlink)
        with self.assertRaisesRegex(ValueError, 'installed version'):
            candidate.installed(result, self.plan, path)
        path.write_text('jobset=v0.12.0\n')
        candidate.installed(result, self.plan, path)

    def test_kourier_only_update_requires_its_provisioning_metadata(self):
        result = {'versions': {'knative': 'knative-v1.15.2'},
                  'defaults': {'KOURIER_VERSION': 'knative-v1.16.0'}}
        path = Path(self.temp.name) / 'installed'
        path.write_text('knative=knative-v1.15.2\n')
        with self.assertRaisesRegex(ValueError, 'kourier'):
            candidate.installed(result, {'group': 'knative'}, path)
        path.write_text('knative=knative-v1.15.2\nkourier=knative-v1.16.0\n')
        candidate.installed(result, {'group': 'knative'}, path)

    def test_manifest_hashes_all_recordings(self):
        result = self.prepare()
        recording = Path(result['directories'][0]) / 'jobset-kind' / 'running.yaml'
        recording.parent.mkdir(parents=True)
        recording.write_text('recording')
        actual = candidate.manifest(result, self.root)
        self.assertEqual(actual['files'][str(recording)], hashlib.sha256(b'recording').hexdigest())
        Path('unexpected').write_text('not a recording')
        with self.assertRaisesRegex(ValueError, 'unexpected changed'):
            candidate.manifest(result, self.root)

    def test_recording_failure_cleans_cluster_and_blocks_evidence(self):
        source = Path(__file__).resolve().parent
        for name in ('record.sh', 'candidate.py'):
            target = self.root / 'hack/e2e/renovate' / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source / name, target)
        self.call('git', 'add', 'hack/e2e/renovate')
        # Keep only the intended pin uncommitted for the post-upgrade hook.
        self.call('git', '-c', 'commit.gpgsign=false', 'commit', '-qm', 'runner')
        self.base = self.call('git', 'rev-parse', 'HEAD').strip()
        artifacts = Path(self.temp.name) / 'evidence'
        artifacts.mkdir()
        (artifacts / 'plan.json').write_text(json.dumps(self.plan))
        binaries = Path(self.temp.name) / 'bin'
        binaries.mkdir()
        commands = {'kind': '#!/bin/sh\nprintf "%s\\n" "$*" >> "$E2E_UPDATE_ARTIFACTS/kind-calls"\n',
                    'make': '#!/bin/sh\nexit 1\n'}
        for name, script in commands.items():
            path = binaries / name
            path.write_text(script)
            path.chmod(0o755)
        env = {**os.environ, 'PATH': str(binaries) + os.pathsep + os.environ['PATH'],
               'GITHUB_ACTIONS': 'true', 'RUNNER_TEMP': self.temp.name, 'GITHUB_RUN_ID': '1', 'GITHUB_RUN_ATTEMPT': '1',
               'E2E_UPDATE_ARTIFACTS': str(artifacts), 'E2E_UPDATE_BASE_SHA': self.base,
               'E2E_UPDATE_GROUP': 'jobset', 'E2E_UPDATE_RUN_URL': 'https://example.com/run'}
        result = subprocess.run(['bash', 'hack/e2e/renovate/record.sh'], env=env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('delete cluster --name renovate-jobset-1-1', (artifacts / 'kind-calls').read_text())
        self.assertFalse((artifacts / 'manifest.json').exists())
        self.assertFalse((Path(self.temp.name) / 'renovate-jobset-kubeconfig').exists())


if __name__ == '__main__':
    unittest.main()
