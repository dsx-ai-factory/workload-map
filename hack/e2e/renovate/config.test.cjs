// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { test } = require('node:test');
const { plan, configuration, groups } = require('./config.cjs');

const env = {
  GITHUB_REPOSITORY: 'example/workload-map',
  E2E_UPDATE_GROUP: 'jobset',
  E2E_UPDATE_BASE_BRANCH: 'validation',
};
const globalEnv = readFileSync(path.resolve(__dirname, '../global.env'), 'utf8');

function config(group) {
  return configuration({ ...env, E2E_UPDATE_GROUP: group });
}

test('every checker group has exactly one automation plan', () => {
  const source = readFileSync(path.resolve(__dirname, '../check-updates.sh'), 'utf8');
  const registered = source.match(/^ALL_OPERATORS=\(([^)]+)\)$/m)[1].split(' ');
  assert.deepEqual(Object.keys(groups), registered);
  let components = 0;
  for (const group of registered) {
    const selected = plan(group, 'main');
    components += selected.pins.length;
    assert.equal(selected.branch, `renovate-operators/${group}/update`);
    assert.ok(selected.workloads.length > 0);
    assert.deepEqual(selected.recordings.map(item => item.operator), selected.workloads);
    for (const pin of selected.pins) assert.equal(selected.defaults[pin.variable], pin.currentValue);
  }
  assert.equal(components, 16);
});

test('current pin matches exactly and preserves its install prefix', () => {
  for (const group of Object.keys(groups)) {
    const selected = plan(group);
    for (const [index, manager] of config(group).customManagers.entries()) {
      const matches = [...globalEnv.matchAll(new RegExp(manager.matchStrings[0], 'g'))];
      assert.equal(matches.length, 1, `${group}: exactly one matching pin`);
      const oldValue = matches[0].groups.currentValue;
      const newText = matches[0][0].replace(oldValue, '9.8.7');
      const prefix = selected.pins[index].currentValue.match(/^[^0-9]*/)[0];
      assert.ok(newText.endsWith(`:-${prefix}9.8.7}"`));
    }
  }
});

test('chart prefixes and release channels exclude unrelated tags', () => {
  const tags = (group, value) => new RegExp(config(group).customManagers[0].extractVersionTemplate).exec(value)?.groups.version;
  assert.equal(tags('kuberay', 'kuberay-operator-1.7.0'), '1.7.0');
  assert.equal(tags('kuberay', 'ray-2.40.0'), undefined);
  assert.equal(tags('milvus', 'milvus-operator-1.4.0'), '1.4.0');
  assert.equal(tags('milvus', 'v1.4.0'), undefined);
  assert.equal(tags('agent-sandbox', 'v1.0.6.post1'), undefined);
  assert.equal(tags('jobset', 'v0.12.0-rc.1'), undefined);
  assert.equal(tags('grove', 'v0.1.0-alpha.11'), '0.1.0-alpha.11');
  assert.equal(tags('grove', 'v0.1.0-alpha.11-rc1'), undefined);
  assert.equal(tags('grove', 'operator/v0.1.0'), undefined);
  assert.equal(config('grove').ignoreUnstable, false);
  assert.equal(config('grove').customManagers[0].versioningTemplate, 'maven');
});

test('migration and retraction policies remain explicit', () => {
  assert.equal(config('kubeflow').packageRules[0].allowedVersions, '<2.0.0');
  const kai = config('kai-scheduler').packageRules[0].allowedVersions;
  assert.equal(kai, '!/^(0\\.20\\.0|0\\.20\\.1)$/');
  assert.deepEqual(plan('kubeflow').recordings, [{ operator: 'kubeflow', versionVariables: ['KUBEFLOW_VERSION', 'MPI_OPERATOR_VERSION'], versionSeparator: '+mpi' }]);
  assert.equal(config('knative').customManagers.length, 2);
});

test('operator run is isolated and waits for recording evidence', () => {
  const cfg = config('jobset');
  assert.equal(cfg.requireConfig, 'ignored');
  assert.deepEqual(cfg.enabledManagers, ['custom.regex']);
  assert.deepEqual(cfg.gitNoVerify, ['commit']);
  assert.equal(cfg.platformCommit, 'disabled');
  assert.equal(Object.hasOwn(cfg.customEnvVariables, 'GIT_CONFIG_GLOBAL'), false);
  assert.equal(cfg.prCreation, 'status-success');
  assert.equal(cfg.draftPR, true);
  assert.equal(cfg.group.branchTopic, 'update');
  assert.deepEqual(cfg.postUpgradeTasks.commands, ['bash hack/e2e/renovate/record.sh']);
  assert.deepEqual(cfg.postUpgradeTasks.fileFilters, ['test/e2e/recorded_data/jobset/**/*.yaml']);
  assert.equal(cfg.automerge, false);
});

test('upstream rejects other base branches and unknown groups', () => {
  assert.throws(() => configuration({ ...env, GITHUB_REPOSITORY: 'dsx-ai-factory/workload-map' }), /main only/);
  assert.throws(() => config('../jobset'), /Unknown operator/);
});

test('shell version overrides cannot change captured source pins', () => {
  const previous = process.env.JOBSET_VERSION;
  process.env.JOBSET_VERSION = 'v99.99.99';
  try {
    const selected = plan('jobset');
    assert.equal(selected.pins[0].currentValue, selected.defaults.JOBSET_VERSION);
    assert.notEqual(selected.pins[0].currentValue, 'v99.99.99');
  } finally {
    if (previous === undefined) delete process.env.JOBSET_VERSION;
    else process.env.JOBSET_VERSION = previous;
  }
});

test('pinned Renovate orders Grove release candidates before their alpha release', { skip: !process.env.RENOVATE_PACKAGE_DIR }, async () => {
  const modulePath = path.join(process.env.RENOVATE_PACKAGE_DIR, 'dist/modules/versioning/maven/index.js');
  const { api } = await import(pathToFileURL(modulePath).href);
  const versions = ['0.1.0-alpha.9', '0.1.0-alpha.10-rc2', '0.1.0-alpha.10-rc10', '0.1.0-alpha.10', '0.1.0-alpha.11-rc1', '0.1.0-alpha.11', '0.1.0'];
  for (let index = 1; index < versions.length; index++) {
    assert.ok(api.isGreaterThan(versions[index], versions[index - 1]), versions[index]);
  }
});

test('fork validation branches cannot share an update head', () => {
  const first = plan('jobset', 'validation-one');
  const second = plan('jobset', 'validation-two');
  assert.notEqual(first.branch, second.branch);
  assert.match(first.branch, /^renovate-operators\/jobset\/[a-f0-9]{8}\/update$/);
  assert.equal(plan('jobset', 'main').branch, 'renovate-operators/jobset/update');
  const cfg = config('jobset');
  assert.equal(cfg.branchPrefix + cfg.group.branchTopic, plan('jobset', env.E2E_UPDATE_BASE_BRANCH).branch);
});
