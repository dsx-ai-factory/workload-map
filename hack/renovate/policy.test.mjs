// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { after, mock, test } from 'node:test';
import { fileURLToPath, pathToFileURL } from 'node:url';

// RENOVATE_PACKAGE_DIR must point to the version pinned in renovate.yaml.
// These tests use internal dist modules, so version bumps can require import updates.
assert(process.env.RENOVATE_PACKAGE_DIR, 'Set RENOVATE_PACKAGE_DIR to the installed Renovate package.');
const runtime = path.resolve(process.env.RENOVATE_PACKAGE_DIR);
const root = fileURLToPath(new URL('../../', import.meta.url));
const require = createRequire(path.join(runtime, 'package.json'));
const { load: parseYaml } = require('js-yaml');
const loadRuntime = (file) => import(pathToFileURL(path.join(runtime, `dist/${file}.js`)).href);
const { init, levels } = await loadRuntime('logger/index');
await init();
levels('stdout', 'fatal');
const { getConfig } = await loadRuntime('config/defaults');
const { resolveConfigPresets } = await loadRuntime('config/presets/index');
const { mergeChildConfig } = await loadRuntime('config/utils');
const { applyPackageRules } = await loadRuntime('util/package-rules/index');
const { groupReleasesIntoBuckets } = await loadRuntime('workers/repository/process/lookup/bucket');
const { filterInternalChecks } = await loadRuntime('workers/repository/process/lookup/filter-checks');
const { generateUpdate } = await loadRuntime('workers/repository/process/lookup/generate');
const { isScheduledNow } = await loadRuntime('workers/repository/update/branch/schedule');
const { extractPackageFile } = await loadRuntime('modules/manager/gomod/extract');
const { applyConstraintsFiltering } = await loadRuntime('modules/datasource/common');
const { api: goVersioning } = await loadRuntime('modules/versioning/gomod/index');
const { checkMinimumReleaseAge } = await loadRuntime('util/minimum-release-age');
// Renovate imports the ESM build; the CommonJS build has a separate clock.
const { Settings } = await import(pathToFileURL(path.join(
  path.dirname(require.resolve('luxon/package.json')), 'build/es6/luxon.mjs',
)).href);

const raw = JSON.parse(readFileSync(path.join(root, 'renovate.json'), 'utf8'));
const config = mergeChildConfig(getConfig(), (await resolveConfigPresets(raw)).config);
const workflow = parseYaml(readFileSync(path.join(root, '.github/workflows/renovate.yaml'), 'utf8'));
const originalNow = Settings.now;
const now = Date.parse('2026-11-17T12:00:00Z');
mock.timers.enable({ apis: ['Date'], now });
Settings.now = () => now;
after(() => {
  Settings.now = originalNow;
  mock.timers.reset();
});

async function dependencyConfig(overrides = {}) {
  const input = {
    manager: 'gomod', datasource: 'go', versioning: 'gomod', depType: 'require',
    packageName: 'example.test/library', depName: 'example.test/library',
    currentVersion: 'v1.2.3', currentValue: 'v1.2.3', baseBranch: 'main',
    ...overrides,
  };
  if (input.isVulnerabilityAlert) input.force = config.vulnerabilityAlerts;
  return applyPackageRules(mergeChildConfig(config, input), 'test');
}

function release(version, ageHours = 72) {
  return { version, releaseTimestamp: new Date(now - ageHours * 60 * 60 * 1000).toISOString() };
}

async function selectedUpdates(input, releases) {
  const dependency = await dependencyConfig(input);
  const buckets = groupReleasesIntoBuckets(dependency, dependency.currentVersion, structuredClone(releases), goVersioning);
  const updates = [];
  for (const [bucket, candidates] of Object.entries(buckets)) {
    candidates.sort((a, b) => goVersioning.sortVersions(a.version, b.version));
    const result = await filterInternalChecks(dependency, goVersioning, bucket, candidates);
    if (!result.release) continue;
    const update = await generateUpdate(dependency, dependency.currentValue, goVersioning, 'replace',
      dependency.currentVersion, bucket, result.release, new Set(releases.map((entry) => entry.version)));
    const effective = await applyPackageRules(mergeChildConfig(dependency, update), 'test-update');
    updates.push({ ...effective, pendingChecks: result.pendingChecks });
  }
  return updates.filter((update) => update.enabled !== false);
}

test('the test runtime matches the Renovate runner version', () => {
  const installed = JSON.parse(readFileSync(path.join(runtime, 'package.json'), 'utf8'));
  assert.equal(installed.version, String(workflow.jobs.renovate.env.RENOVATE_VERSION));
});

test('release branches preserve patches when newer minors exist', async (t) => {
  for (const baseBranch of ['v0.2', 'release-0.3', 'v1.10']) {
    for (const depName of ['example.test/library', 'k8s.io/apimachinery']) {
      await t.test(`${baseBranch}: ${depName}`, async () => {
        const updates = await selectedUpdates({ baseBranch, depName, packageName: depName },
          [release('v1.2.4'), release('v1.3.0')]);
        assert.deepEqual(updates.map((update) => update.newVersion), ['v1.2.4']);
        assert.equal(updates[0].pendingChecks, false);
      });
    }
  }
  const main = await selectedUpdates({}, [release('v1.2.4'), release('v1.3.0')]);
  assert.deepEqual(main.map((update) => update.newVersion), ['v1.3.0']);
});

test('routine Go upgrades wait exactly 48 hours', async (t) => {
  for (const updateType of ['major', 'minor', 'patch']) {
    const dependency = await dependencyConfig({ updateType });
    for (const [ageHours, pending] of [[0, true], [47.999, true], [48, false], [72, false]]) {
      await t.test(`${updateType} at ${ageHours} hours`, () => {
        const result = checkMinimumReleaseAge(dependency, release('v1.3.0', ageHours).releaseTimestamp);
        assert.equal(result.minimumReleaseAgeMs, 48 * 60 * 60 * 1000);
        assert.equal(result.isPending, pending);
      });
    }
    assert.equal(checkMinimumReleaseAge(dependency).isPending, true, 'Unknown release age must remain pending');
  }
});

test('candidate selection keeps young releases pending and chooses eligible older releases', async () => {
  const young = await selectedUpdates({}, [release('v1.2.4', 24)]);
  assert.equal(young[0].pendingChecks, true);
  const mixed = await selectedUpdates({}, [release('v1.2.4', 72), release('v1.2.5', 24)]);
  assert.equal(mixed[0].newVersion, 'v1.2.4');
  assert.equal(mixed[0].pendingChecks, false);
  const missing = await selectedUpdates({}, [{ version: 'v1.2.4' }]);
  assert.equal(missing[0].pendingChecks, true);
});

test('compatible security updates bypass the wait and release-branch minor restriction', async (t) => {
  for (const baseBranch of ['main', 'v0.3', 'release-0.3']) {
    await t.test(baseBranch, async () => {
      const updates = await selectedUpdates({ baseBranch, isVulnerabilityAlert: true }, [release('v1.3.0', 1)]);
      assert.equal(updates[0].newVersion, 'v1.3.0');
      assert.equal(updates[0].pendingChecks, false);
      assert.equal(updates[0].minimumReleaseAge, null);
      assert.deepEqual(updates[0].schedule, []);
      assert.equal(updates[0].constraintsFiltering, 'strict');
      const major = await dependencyConfig({ baseBranch, isVulnerabilityAlert: true, updateType: 'major' });
      assert.equal(major.enabled, false);
    });
  }
});

test('Go compatibility filtering uses each module directive before update classification', async (t) => {
  for (const goMinor of [26, 27]) {
    await t.test(`Go 1.${goMinor}`, async () => {
      const extracted = extractPackageFile(`module example.test/project\n\ngo 1.${goMinor}.3\n\nrequire example.test/library v1.2.3\n`);
      for (const isVulnerabilityAlert of [false, true]) {
        const dependency = await dependencyConfig({ isVulnerabilityAlert });
        const result = applyConstraintsFiltering({ releases: [
          { version: 'v1.2.4', constraints: { '%goMod': [`1.${goMinor - 1}.0`] } },
          { version: 'v1.2.5', constraints: { '%goMod': [`1.${goMinor}.9`] } },
          { version: 'v1.2.6', constraints: { '%goMod': [`1.${goMinor + 1}.0`] } },
          { version: 'v1.2.7' },
        ] }, { ...dependency, constraints: extracted.extractedConstraints,
          constraintsVersioning: extracted.constraintsVersioning });
        assert.deepEqual(result.releases.map((entry) => entry.version), ['v1.2.4', 'v1.2.5', 'v1.2.7']);
      }
    });
  }
});

test('Go toolchain and other ecosystems do not inherit the Go module wait', async (t) => {
  for (const [manager, datasource] of [
    ['gomod', 'golang-version'], ['regex', 'golang-version'], ['npm', 'npm'],
    ['github-actions', 'github-tags'], ['dockerfile', 'docker'],
  ]) {
    await t.test(`${manager}/${datasource}`, async () => {
      const dependency = await dependencyConfig({ manager, datasource, depName: 'example', packageName: 'example', updateType: 'patch' });
      assert.equal(checkMinimumReleaseAge(dependency, release('v1.2.4', 0).releaseTimestamp).isPending, false);
    });
  }
  const toolchain = await dependencyConfig({ datasource: 'golang-version', depName: 'go', packageName: 'go', updateType: 'minor' });
  assert.equal(toolchain.groupName, 'Go toolchain');
});

test('quarterly major windows and the security major restriction remain effective', async () => {
  const major = await dependencyConfig({ updateType: 'major' });
  assert.equal(major.enabled, true);
  assert.equal(major.updateNotScheduled, false);
  try {
    for (const [date, scheduled] of [
      ['2027-01-01T00:00:00Z', true], ['2027-01-01T23:59:59Z', true],
      ['2027-01-02T00:00:00Z', false], ['2027-02-01T03:00:00Z', false],
      ['2027-04-01T03:00:00Z', true], ['2027-07-01T03:00:00Z', true],
      ['2027-10-01T03:00:00Z', true], ['2026-11-17T12:00:00Z', false],
    ]) {
      Settings.now = () => Date.parse(date);
      assert.equal(isScheduledNow(major), scheduled, date);
    }
  } finally {
    Settings.now = () => now;
  }
  assert.equal((await dependencyConfig({ baseBranch: 'v0.3', updateType: 'major' })).enabled, false);
});

test('all ecosystems retain license generation and permitted commands', async (t) => {
  const allowed = JSON.parse(workflow.jobs.renovate.steps.find((step) => step.name === 'Run Renovate').env.RENOVATE_ALLOWED_COMMANDS);
  for (const [manager, datasource] of [['gomod', 'go'], ['npm', 'npm'], ['github-actions', 'github-tags'], ['dockerfile', 'docker']]) {
    await t.test(manager, async () => {
      const dependency = await dependencyConfig({ manager, datasource, updateType: 'patch' });
      assert(dependency.postUpgradeTasks.commands.some((command) => command.split(' ').includes('generate-licenses')));
    });
  }
  for (const baseBranch of ['main', 'v0.2', 'v0.3', 'release-0.3']) {
    const dependency = await dependencyConfig({ baseBranch, updateType: 'patch' });
    for (const command of dependency.postUpgradeTasks.commands) {
      assert(allowed.some((pattern) => new RegExp(pattern).test(command)), `${baseBranch}: ${command}`);
    }
  }
});
