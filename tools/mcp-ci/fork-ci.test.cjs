const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const YAML = require('yaml');

const root = path.resolve(__dirname, '../..');
const read = name => YAML.parse(fs.readFileSync(path.join(root, '.github/workflows', name), 'utf8'));
const gate = read('fork-approval.yaml');
const worker = read('fork-ci.yaml');
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
const script = job => job.steps.find(step => step.uses === 'actions/github-script@v7').with.script;
const sha = 'a'.repeat(40);
const mergeSHA = 'c'.repeat(40);
const repo = {owner: 'SigNoz', repo: 'signoz-mcp-server'};
const approvalURL = `https://github.com/${repo.owner}/${repo.repo}/actions/runs/100`;
// These contexts come from the main branch ruleset, not the workflow implementation.
const requiredChecks = ['test / go', 'fmt / go', 'deps / go', 'build / go', 'lint / go', 'contract'];

function fixture() {
  const pr = {
    number: 317, state: 'open', draft: false, base: {ref: 'main'}, merge_commit_sha: mergeSHA,
    head: {sha, repo: {full_name: 'contributor/signoz-mcp-server'}},
    user: {login: 'contributor'}, labels: [{name: 'safe-to-test'}],
  };
  const context = {
    repo, actor: 'maintainer', runId: 100, serverUrl: 'https://github.com',
    payload: {
      repository: {default_branch: 'main'}, action: 'labeled', label: {name: 'safe-to-test'},
      pull_request: structuredClone(pr),
      inputs: {pr: '317', sha, merge_sha: mergeSHA, draft: 'false', approval_run: '100'},
    },
  };
  const state = {
    pr, context, permission: 'write', statuses: new Map(), calls: [], outputs: {},
    source: {event: 'pull_request_target', path: '.github/workflows/fork-approval.yaml', display_title: `approve-fork #317 ${sha} ready`, html_url: approvalURL},
    env: {PR_NUMBER: '317', APPROVED_SHA: sha, APPROVED_MERGE_SHA: mergeSHA, APPROVED_DRAFT: 'false', APPROVAL_URL: approvalURL, CHECKS_RESULT: 'success', E2E_RESULT: 'success'},
  };
  state.statuses.set('fork-approval', {context: 'fork-approval', state: 'pending', target_url: approvalURL});
  state.github = {rest: {
    pulls: {get: async () => ({data: state.pr})},
    repos: {
      getCollaboratorPermissionLevel: async () => ({data: {permission: state.permission}}),
      createCommitStatus: async params => {
        state.calls.push(['status', params]);
        state.statuses.set(params.context, params);
      },
      getCombinedStatusForRef: async () => ({data: {statuses: [...state.statuses.values()]}}),
    },
    issues: {removeLabel: async params => {
      state.calls.push(['remove', params]);
      if (state.removeError) throw Object.assign(new Error('label API error'), {status: state.removeError});
      state.pr.labels = [];
    }},
    actions: {
      createWorkflowDispatch: async params => state.calls.push(['dispatch', params]),
      getWorkflowRun: async () => ({data: state.source}),
    },
  }};
  state.run = async job => new AsyncFunction('context', 'github', 'core', 'process', script(job))(
    context, state.github, {setOutput: (key, value) => {state.outputs[key] = value;}}, {env: state.env},
  );
  return state;
}

function gateMatches(state) {
  const expression = gate.jobs.approval.if
    .replaceAll('github.event.', 'event.')
    .replaceAll('github.repository', 'repository');
  return new Function('event', 'repository', 'contains', 'fromJSON', `return (${expression});`)(
    state.context.payload, `${repo.owner}/${repo.repo}`, (items, value) => items.includes(value), JSON.parse,
  );
}

test('maintainer label dispatches exactly the approved head through the default branch', async () => {
  const state = fixture();
  assert.equal(gateMatches(state), true);
  await state.run(gate.jobs.approval);
  const dispatch = state.calls.find(([kind]) => kind === 'dispatch')[1];
  assert.equal(dispatch.ref, 'main');
  assert.equal(dispatch.workflow_id, 'fork-ci.yaml');
  assert.deepEqual(dispatch.inputs, {pr: '317', sha, merge_sha: mergeSHA, draft: 'false', approval_run: '100'});
  for (const name of requiredChecks) assert.equal(state.statuses.get(name).state, 'pending');
  assert.equal(state.calls.filter(([kind]) => kind === 'remove').length, 0);
});

for (const action of ['opened', 'synchronize', 'reopened', 'ready_for_review', 'unlabeled']) {
  test(`${action} cannot reuse an old label or a passing check run`, async () => {
    const state = fixture();
    state.context.payload.action = action;
    if (action === 'unlabeled') state.pr.labels = [];
    assert.equal(gateMatches(state), true);
    await state.run(gate.jobs.approval);
    assert.equal(state.calls.some(([kind]) => kind === 'dispatch'), false);
    for (const name of requiredChecks) assert.equal(state.statuses.get(name).state, 'pending');
    assert.equal(state.pr.labels.length, 0);
  });
}

for (const action of ['labeled', 'unlabeled']) {
  test(`unrelated ${action} events leave approval and results unchanged`, () => {
    const state = fixture();
    state.context.payload.action = action;
    state.context.payload.label.name = 'bug';
    assert.equal(gateMatches(state), false);
  });
}

test('internal PRs do not enter the fork dispatcher; Dependabot does', () => {
  const state = fixture();
  state.context.payload.pull_request.head.repo.full_name = `${repo.owner}/${repo.repo}`;
  assert.equal(gateMatches(state), false);
  state.context.payload.pull_request.user.login = 'dependabot[bot]';
  assert.equal(gateMatches(state), true);
});

for (const change of [s => {s.pr.head.sha = 'b'.repeat(40);}, s => {s.pr.draft = true;}, s => {s.pr.state = 'closed';}, s => {s.pr.labels = []; }]) {
  test('outdated approval event cannot authorize or reset the current PR', async () => {
    const state = fixture();
    change(state);
    await state.run(gate.jobs.approval);
    assert.deepEqual(state.calls, []);
  });
}

test('label by an actor without write permission cannot dispatch', async () => {
  const state = fixture();
  state.permission = 'read';
  await assert.rejects(state.run(gate.jobs.approval), /maintainer/);
  assert.deepEqual(state.calls, []);
});

for (const code of [404, 403]) {
  test(`label reset handles API ${code} without hiding permission failures`, async () => {
    const state = fixture();
    state.context.payload.action = 'synchronize';
    state.removeError = code;
    if (code === 403) await assert.rejects(state.run(gate.jobs.approval), /API error/);
    else await state.run(gate.jobs.approval);
  });
}

test('worker accepts current approval and forwards only validated metadata', async () => {
  const state = fixture();
  await state.run(worker.jobs.authorize);
  assert.deepEqual(state.outputs, {sha, merge_sha: mergeSHA, draft: 'false', pr: '317', approval_url: approvalURL});
});

const invalidApprovals = {
  'malformed input': s => {s.context.payload.inputs.sha = 'main';},
  'untrusted trigger': s => {s.source.event = 'pull_request';},
  'different workflow': s => {s.source.path = '.github/workflows/fake.yaml';},
  'reset event': s => {s.source.display_title = `reset-fork #317 ${sha}`;},
  'different commit': s => {s.pr.head.sha = 'b'.repeat(40);},
  'changed merge revision': s => {s.pr.merge_commit_sha = 'd'.repeat(40);},
  'draft became ready': s => {s.pr.draft = true;},
  'closed PR': s => {s.pr.state = 'closed';},
  'revoked label': s => {s.pr.labels = [];},
  'different target branch': s => {s.pr.base.ref = 'release';},
  'newer approval': s => {s.statuses.get('fork-approval').target_url = `${approvalURL}1`;},
  'already completed approval': s => {s.statuses.get('fork-approval').state = 'success';},
};
for (const [name, change] of Object.entries(invalidApprovals)) {
  test(`worker rejects ${name} before any checkout`, async () => {
    const state = fixture();
    change(state);
    await assert.rejects(state.run(worker.jobs.authorize));
    assert.deepEqual(state.outputs, {});
  });
}

for (const [checks, e2e] of [['success', 'success'], ['failure', 'success'], ['success', 'failure'], ['cancelled', 'skipped']]) {
  test(`report maps checks=${checks}, E2E=${e2e} to commit statuses`, async () => {
    const state = fixture();
    state.context.runId = 200;
    state.env.CHECKS_RESULT = checks;
    state.env.E2E_RESULT = e2e;
    await state.run(worker.jobs.report);
    for (const name of requiredChecks) assert.equal(state.statuses.get(name).state, checks === 'success' ? 'success' : 'failure');
    assert.equal(state.statuses.get('repo-docs').state, checks === 'success' ? 'success' : 'failure');
    assert.equal(state.statuses.get('e2e').state, e2e === 'success' ? 'success' : 'failure');
    assert.equal(state.statuses.get('fork-approval').state, checks === 'success' && e2e === 'success' ? 'success' : 'failure');
    assert.deepEqual([...new Set(state.calls.map(([, status]) => status.sha))], [sha, mergeSHA]);
  });
}

for (const key of ['different commit', 'changed merge revision', 'draft became ready', 'closed PR', 'revoked label', 'newer approval']) {
  test(`late report cannot overwrite ${key}`, async () => {
    const state = fixture();
    invalidApprovals[key](state);
    await state.run(worker.jobs.report);
    assert.deepEqual(state.calls, []);
  });
}

// Drift pins protect the trust boundary, not incidental workflow formatting.
test('fork execution cannot inherit secrets, write tokens, caches, or reporter files', () => {
  assert.deepEqual(Object.keys(gate.on), ['pull_request_target']);
  assert.deepEqual(Object.keys(worker.on), ['workflow_dispatch']);
  assert.equal(worker['cache-mode'], 'none');
  assert.deepEqual(worker.permissions, {});
  for (const name of ['checks', 'e2e']) {
    const job = worker.jobs[name];
    assert.deepEqual(job.permissions, {contents: 'read'});
    assert.equal(job.needs, 'authorize');
    assert.doesNotMatch(JSON.stringify(job), /secrets\./);
    const checkout = job.steps.find(step => step.uses === 'actions/checkout@v4');
    assert.equal(checkout.with.ref, '${{ needs.authorize.outputs.sha }}');
    assert.equal(checkout.with['persist-credentials'], false);
  }
  for (const job of [gate.jobs.approval, worker.jobs.authorize, worker.jobs.report]) {
    assert.equal(job.steps.every(step => step.uses === 'actions/github-script@v7'), true);
  }
  assert.equal(gate.jobs.approval.concurrency.group, 'fork-status-${{ github.event.number }}');
  assert.equal(worker.jobs.report.concurrency.group, 'fork-status-${{ inputs.pr }}');
  assert.equal(worker.jobs.report.concurrency['cancel-in-progress'], false);
  assert.match(worker.jobs.checks.steps.at(-1).run, /check-repo-docs READY=1/);
});
