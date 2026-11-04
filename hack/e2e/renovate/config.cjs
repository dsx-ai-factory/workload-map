// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

const { execFileSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { readFileSync } = require('node:fs');
const path = require('node:path');

const root = path.resolve(__dirname, '../../..');
const groups = {
  'cert-manager': { pins: { 'cert-manager': 'CERT_MANAGER_VERSION' }, workloads: ['kserve'], webhookMode: 'cert-manager' },
  'fake-gpu-operator': { pins: { 'fake-gpu-operator': 'FAKE_GPU_VERSION' }, workloads: ['grove'] },
  'kai-scheduler': { pins: { 'kai-scheduler': 'KAI_SCHEDULER_VERSION' }, workloads: ['grove', 'dynamo'] },
  lws: { pins: { lws: 'LWS_VERSION' } },
  jobset: { pins: { jobset: 'JOBSET_VERSION' } },
  kuberay: { pins: { kuberay: 'KUBERAY_VERSION' } },
  kubeflow: { pins: { kubeflow: 'KUBEFLOW_VERSION', 'mpi-operator': 'MPI_OPERATOR_VERSION' } },
  knative: { pins: { knative: 'KNATIVE_VERSION', kourier: 'KOURIER_VERSION' } },
  kserve: { pins: { kserve: 'KSERVE_VERSION' } },
  milvus: { pins: { milvus: 'MILVUS_OPERATOR_VERSION' } },
  grove: { pins: { grove: 'GROVE_VERSION' } },
  dynamo: { pins: { dynamo: 'DYNAMO_VERSION' } },
  nim: { pins: { nim: 'NIM_OPERATOR_VERSION' } },
  'agent-sandbox': { pins: { 'agent-sandbox': 'AGENT_SANDBOX_VERSION' } },
};

function plan(group, baseBranch = process.env.E2E_UPDATE_BASE_BRANCH || 'main') {
  if (!Object.hasOwn(groups, group)) throw new Error(`Unknown operator group: ${group}`);
  const selected = groups[group];
  const contents = readFileSync(path.join(root, 'hack/e2e/global.env'), 'utf8');
  const defaults = Object.fromEntries([...contents.matchAll(/^([A-Z_]+)="\$\{\1:-([^$"}]+)\}"$/gm)].map(m => [m[1], m[2]]));
  const env = { ...process.env, E2E_OPERATOR_UPDATES_BIN: path.join(__dirname, 'capture.cjs') };
  // A caller's shell overrides must not change the pins captured from this checkout.
  for (const variable of Object.keys(defaults)) delete env[variable];
  const directory = ['cert-manager', 'fake-gpu-operator', 'kai-scheduler'].includes(group) ? 'infra' : 'operators';
  const args = JSON.parse(execFileSync('bash', [path.join(root, 'hack/e2e', directory, group, 'check-updates.sh')], { env, encoding: 'utf8' }));
  const pins = [];
  const excludedTags = [];
  for (let index = 0; index < args.length;) {
    const flag = args[index++];
    if (flag === '--exclude-tag' && index < args.length) {
      excludedTags.push(args[index++]);
    } else if (flag === '--operator' && index + 6 <= args.length) {
      const [name, currentValue, repository, prefix, channel, note] = args.slice(index, index + 6);
      index += 6;
      const variable = selected.pins[name];
      if (!variable || defaults[variable] !== currentValue || !['stable', 'alpha'].includes(channel)) {
        throw new Error(`Unmapped release policy for ${name}`);
      }
      pins.push({ name, variable, currentValue, repository, prefix, channel, note });
    } else {
      throw new Error(`Unsupported checker flag: ${flag}`);
    }
  }
  if (pins.length !== Object.keys(selected.pins).length || new Set(pins.map(pin => pin.name)).size !== pins.length) {
    throw new Error(`Incomplete release policy for ${group}`);
  }
  for (const pin of pins) pin.excludedTags = excludedTags;
  const workloads = selected.workloads || [group];
  const recordings = workloads.map(operator => ({
    operator,
    versionVariables: operator === 'kubeflow' ? ['KUBEFLOW_VERSION', 'MPI_OPERATOR_VERSION'] : [Object.values(groups[operator].pins)[0]],
    versionSeparator: operator === 'kubeflow' ? '+mpi' : '',
  }));
  const suffix = baseBranch === 'main' ? '' : createHash('sha256').update(baseBranch).digest('hex').slice(0, 8) + '/';
  return { group, pins, workloads, recordings, webhookMode: selected.webhookMode || 'auto', branch: `renovate-operators/${group}/${suffix}update`, defaults };
}

function escapeRegex(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function configuration(env = process.env) {
  const selected = plan(env.E2E_UPDATE_GROUP, env.E2E_UPDATE_BASE_BRANCH);
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(env.GITHUB_REPOSITORY || '')) throw new Error('Set GITHUB_REPOSITORY');
  const baseBranch = env.E2E_UPDATE_BASE_BRANCH;
  if (!baseBranch || /[\s~^:?*\[\\]/.test(baseBranch)) throw new Error('Set a valid E2E_UPDATE_BASE_BRANCH');
  if (env.GITHUB_REPOSITORY === 'dsx-ai-factory/workload-map' && baseBranch !== 'main') throw new Error('Upstream operator updates target main only');
  const version = '[0-9]+\\.[0-9]+\\.[0-9]+(?:-[0-9A-Za-z.-]+)?';
  const customManagers = selected.pins.map(pin => {
    const pinPrefix = pin.currentValue.match(/^[^0-9]*/)[0];
    const release = '[0-9]+\\.[0-9]+\\.[0-9]+' + (pin.channel === 'alpha' ? '(?:-alpha\\.[0-9]+)?' : '');
    return {
      customType: 'regex',
      managerFilePatterns: ['/^hack\\/e2e\\/global\\.env$/'],
      matchStrings: [`(?:^|\\n)${pin.variable}="\\$\\{${pin.variable}:-${escapeRegex(pinPrefix)}(?<currentValue>${version})\\}"`],
      depNameTemplate: pin.repository,
      datasourceTemplate: 'github-tags',
      // Grove promotes alpha.N-rcM to alpha.N. Maven's qualifier ordering
      // matches that policy; SemVer ranks the RC above the promoted alpha.
      versioningTemplate: pin.channel === 'alpha' ? 'maven' : 'semver',
      extractVersionTemplate: `^${escapeRegex(pin.prefix)}(?<version>${release})$`,
      depTypeTemplate: 'e2e-operator',
    };
  });
  const policyRules = selected.pins.flatMap(pin => {
    const rules = [];
    if (pin.name === 'kubeflow') rules.push({ matchPackageNames: [pin.repository], allowedVersions: '<2.0.0' });
    if (pin.excludedTags.length) {
      const excluded = pin.excludedTags.map(tag => {
        if (!tag.startsWith(pin.prefix)) throw new Error(`Unexpected excluded tag ${tag}`);
        return escapeRegex(tag.slice(pin.prefix.length));
      });
      rules.push({ matchPackageNames: [pin.repository], allowedVersions: `!/^(${excluded.join('|')})$/` });
    }
    return rules;
  });
  const forwarded = [
    'GITHUB_ACTIONS', 'GITHUB_REPOSITORY', 'GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT', 'GITHUB_STEP_SUMMARY',
    'E2E_UPDATE_GROUP', 'E2E_UPDATE_BASE_BRANCH', 'E2E_UPDATE_BASE_SHA', 'E2E_UPDATE_ARTIFACTS', 'E2E_UPDATE_RUN_URL',
    'RUNNER_TEMP',
  ];
  return {
    platform: 'github',
    repositories: [env.GITHUB_REPOSITORY],
    onboarding: false,
    requireConfig: 'ignored',
    forkProcessing: 'enabled',
    enabledManagers: ['custom.regex'],
    includePaths: ['hack/e2e/global.env'],
    baseBranchPatterns: [baseBranch],
    branchPrefix: selected.branch.slice(0, -'update'.length),
    additionalBranchPrefix: '',
    branchTopic: 'update',
    groupName: `e2e ${selected.group}`,
    groupSlug: selected.group,
    group: { branchTopic: 'update' },
    separateMajorMinor: false,
    separateMinorPatch: false,
    // The launcher publishes this status and opens the PR after verifying the
    // pushed commit against the exact bytes produced by the recording job.
    prCreation: 'status-success',
    draftPR: true,
    dependencyDashboard: false,
    pruneStaleBranches: false,
    automerge: false,
    ignoreTests: false,
    ignoreUnstable: !selected.pins.some(pin => pin.channel === 'alpha'),
    binarySource: 'global',
    platformCommit: 'disabled',
    gitNoVerify: ['commit'],
    executionTimeout: 45,
    allowedCommands: ['^bash hack/e2e/renovate/record\\.sh$'],
    customEnvVariables: Object.fromEntries(forwarded.map(key => [key, env[key] || ''])),
    semanticCommits: 'enabled',
    semanticCommitType: 'build',
    semanticCommitScope: 'e2e',
    commitBody: 'Signed-off-by: {{{gitAuthor}}}',
    customManagers,
    packageRules: policyRules,
    postUpgradeTasks: {
      commands: ['bash hack/e2e/renovate/record.sh'],
      executionMode: 'branch',
      fileFilters: selected.workloads.map(workload => `test/e2e/recorded_data/${workload}/**/*.yaml`),
    },
  };
}

if (require.main === module) {
  const [command, group] = process.argv.slice(2);
  if (command === '--plan') process.stdout.write(JSON.stringify(plan(group)) + '\n');
  else if (command === '--matrix') {
    if (group !== 'all' && !Object.hasOwn(groups, group)) throw new Error(`Unknown operator group: ${group}`);
    process.stdout.write(JSON.stringify(group === 'all' ? Object.keys(groups) : [group]) + '\n');
  } else throw new Error('Usage: config.cjs --plan GROUP | --matrix GROUP|all');
} else if (process.env.NODE_TEST_CONTEXT) {
  module.exports = { plan, configuration, groups };
} else {
  module.exports = configuration();
}
