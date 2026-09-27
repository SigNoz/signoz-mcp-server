const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const YAML = require('yaml');

const root = path.resolve(__dirname, '../..');
const read = name => YAML.parse(fs.readFileSync(path.join(root, '.github/workflows', name), 'utf8'));
const gate = read('fork-approval.yaml');
const worker = read('fork-ci.yaml');
const reconciler = read('fork-reconcile.yaml');
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
    number: 317, state: 'open', draft: false, base: {ref: 'main'}, mergeable: true, merge_commit_sha: mergeSHA,
    head: {sha, repo: {full_name: 'contributor/signoz-mcp-server'}},
    user: {login: 'contributor'}, labels: [{name: 'safe-to-test'}],
  };
  const context = {
    repo, actor: 'maintainer', runId: 100, runNumber: 100, serverUrl: 'https://github.com',
    payload: {
      repository: {default_branch: 'main'}, action: 'labeled', label: {name: 'safe-to-test'},
      changes: {base: {ref: {from: ''}}},
      pull_request: structuredClone(pr),
      inputs: {pr: '317', sha, merge_sha: mergeSHA, draft: 'false', approval_run: '100'},
    },
  };
  const state = {
    pr, context, permission: 'write', statuses: new Map(), calls: [], outputs: {}, runs: [],
    source: {run_number: 100, created_at: '2026-09-27T18:00:00Z', event: 'pull_request_target', path: '.github/workflows/fork-approval.yaml', display_title: `approve-fork #317 ${sha} ready`, html_url: approvalURL},
    env: {PR_NUMBER: '317', APPROVED_SHA: sha, APPROVED_MERGE_SHA: mergeSHA, APPROVED_DRAFT: 'false', APPROVAL_URL: approvalURL, CHECKS_RESULT: 'success', E2E_RESULT: 'success'},
  };
  state.status = (name, revision = sha) => state.statuses.get(`${revision}:${name}`);
  for (const revision of [sha, mergeSHA]) {
    for (const name of ['contract', 'fork-approval']) {
      state.statuses.set(`${revision}:${name}`, {sha: revision, context: name, state: 'pending', target_url: approvalURL});
    }
  }
  state.github = {rest: {
    pulls: {
      get: async () => ({data: state.readPR ? state.readPR() : state.pr}),
      list: async () => ({data: state.prs || [state.pr]}),
    },
    repos: {
      getCollaboratorPermissionLevel: async () => ({data: {permission: state.permission}}),
      createCommitStatus: async params => {
        state.calls.push(['status', params]);
        if (state.statusError?.(params)) throw new Error('status API error');
        state.statuses.set(`${params.sha}:${params.context}`, params);
      },
      getCombinedStatusForRef: async ({ref}) => ({data: {statuses: [...state.statuses.values()].filter(status => status.sha === ref)}}),
    },
    issues: {removeLabel: async params => {
      state.calls.push(['remove', params]);
      if (state.removeError) throw Object.assign(new Error('label API error'), {status: state.removeError});
      state.pr.labels = [];
    }},
    actions: {
      createWorkflowDispatch: async params => state.calls.push(['dispatch', params]),
      getWorkflowRun: async () => ({data: state.source}),
      listWorkflowRuns: async () => ({data: {workflow_runs: state.runs}}),
      reRunWorkflow: async params => {
        state.calls.push(['rerun', params]);
        if (state.rerunError) throw new Error('rerun API error');
      },
    },
  }};
  state.github.paginate = async (method, params) => {
    const {data} = await method(params);
    return data.workflow_runs || data;
  };
  state.run = async job => new AsyncFunction('context', 'github', 'core', 'process', 'setTimeout', script(job))(
    {...context, runId: job === gate.jobs.approval ? context.runId : 200}, state.github,
    {setOutput: (key, value) => {state.outputs[key] = value;}}, {env: state.env}, callback => callback(),
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
  for (const name of requiredChecks) assert.equal(state.status(name).state, 'pending');
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
    for (const name of requiredChecks) assert.equal(state.status(name).state, 'pending');
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
  assert.deepEqual(state.outputs, {sha, merge_sha: mergeSHA, revisions: JSON.stringify([sha, mergeSHA]), draft: 'false', pr: '317', approval_url: approvalURL});
});

test('a failed approved run can be retried without reapplying the label', async () => {
  const state = fixture();
  state.env.E2E_RESULT = 'failure';
  await state.run(worker.jobs.report);
  assert.equal(state.status('fork-approval').state, 'failure');
  await state.run(worker.jobs.authorize);
  state.env.E2E_RESULT = 'success';
  await state.run(worker.jobs.report);
  assert.equal(state.status('fork-approval').state, 'success');
});

const invalidApprovals = {
  'malformed input': s => {s.context.payload.inputs.sha = 'main';},
  'missing merge revision': s => {s.context.payload.inputs.merge_sha = '';},
  'untrusted trigger': s => {s.source.event = 'pull_request';},
  'different workflow': s => {s.source.path = '.github/workflows/fake.yaml';},
  'reset event': s => {s.source.display_title = `reset-fork #317 ${sha}`;},
  'different commit': s => {s.pr.head.sha = 'b'.repeat(40);},
  'draft became ready': s => {s.pr.draft = true;},
  'closed PR': s => {s.pr.state = 'closed';},
  'revoked label': s => {s.pr.labels = [];},
  'different target branch': s => {s.pr.base.ref = 'release';},
  'partial reset on merge revision': s => {s.status('contract', mergeSHA).target_url = `${approvalURL}1`;},
  'newer approval': s => {s.status('contract').target_url = `${approvalURL}1`;},
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
    for (const name of requiredChecks) assert.equal(state.status(name).state, checks === 'success' && (name !== 'contract' || e2e === 'success') ? 'success' : 'failure');
    assert.equal(state.status('repo-docs').state, checks === 'success' ? 'success' : 'failure');
    assert.equal(state.status('e2e').state, e2e === 'success' ? 'success' : 'failure');
    assert.equal(state.status('fork-approval').state, checks === 'success' && e2e === 'success' ? 'success' : 'failure');
    assert.deepEqual([...new Set(state.calls.map(([, status]) => status.sha))], [sha, mergeSHA]);
  });
}

for (const key of ['different commit', 'draft became ready', 'closed PR', 'revoked label', 'different target branch', 'partial reset on merge revision', 'newer approval']) {
  test(`late report cannot overwrite ${key}`, async () => {
    const state = fixture();
    invalidApprovals[key](state);
    await state.run(worker.jobs.report);
    assert.deepEqual(state.calls, []);
  });
}

for (const [from, to] of [['main', 'release'], ['release', 'main']]) {
  test(`retargeting from ${from} to ${to} resets approval and blocks old reports`, async () => {
    const state = fixture();
    await state.run(worker.jobs.report);
    state.calls.length = 0;
    state.context.payload.action = 'edited';
    state.context.payload.changes.base.ref.from = from;
    state.context.payload.pull_request.base.ref = to;
    state.pr.base.ref = to;
    state.context.runId = 101;
    state.context.runNumber = 101;
    assert.equal(gateMatches(state), true);
    await state.run(gate.jobs.approval);
    assert.equal(state.status('contract').state, 'pending');
    assert.deepEqual(state.pr.labels, []);
    assert.equal(state.calls.some(([kind]) => kind === 'dispatch'), false);
    state.calls.length = 0;
    await state.run(worker.jobs.report);
    assert.deepEqual(state.calls, []);
  });
}

test('editing a title or body does not reset approval', () => {
  const state = fixture();
  state.context.payload.action = 'edited';
  assert.equal(gateMatches(state), false);
});

test('every revision receiving success is selected for checks and E2E', async () => {
  const state = fixture();
  await state.run(worker.jobs.authorize);
  const tested = JSON.parse(state.outputs.revisions);
  assert.deepEqual(tested, [sha, mergeSHA]);
  await state.run(worker.jobs.report);
  const reported = [...new Set(state.calls.filter(([kind]) => kind === 'status').map(([, status]) => status.sha))];
  assert.deepEqual(reported, tested);
});

test('a main update preserves results only for the frozen tested revisions', async () => {
  const state = fixture();
  const newMerge = 'd'.repeat(40);
  state.pr.merge_commit_sha = newMerge;
  await state.run(worker.jobs.authorize);
  assert.deepEqual(JSON.parse(state.outputs.revisions), [sha, mergeSHA]);
  await state.run(worker.jobs.report);
  assert.equal(state.status('contract').state, 'success');
  assert.equal(state.status('contract', mergeSHA).state, 'success');
  assert.equal(state.status('contract', newMerge), undefined);
});

test('duplicate head and merge revision runs once', async () => {
  const state = fixture();
  state.context.payload.inputs.merge_sha = sha;
  await state.run(worker.jobs.authorize);
  assert.deepEqual(JSON.parse(state.outputs.revisions), [sha]);
});

test('approval waits for GitHub to compute and gate the merge revision', async () => {
  const state = fixture();
  let reads = 0;
  state.readPR = () => ++reads < 3 ? {...state.pr, mergeable: null, merge_commit_sha: null} : state.pr;
  await state.run(gate.jobs.approval);
  const dispatch = state.calls.find(([kind]) => kind === 'dispatch')[1];
  assert.equal(dispatch.inputs.merge_sha, mergeSHA);
  for (const revision of [sha, mergeSHA]) assert.equal(state.status('contract', revision).state, 'pending');
});

for (const mergeable of [null, false]) {
  test(`approval without a computed merge (${mergeable}) never dispatches a head-only run`, async () => {
    const state = fixture();
    state.pr.mergeable = mergeable;
    state.pr.merge_commit_sha = null;
    await assert.rejects(state.run(gate.jobs.approval), /merge revision/);
    assert.equal(state.calls.some(([kind]) => kind === 'dispatch'), false);
    assert.equal(state.status('contract').state, 'pending');
  });
}

test('a head update while computing the merge cannot approve the new revision', async () => {
  const state = fixture();
  let reads = 0;
  state.readPR = () => ++reads === 1
    ? {...state.pr, mergeable: null, merge_commit_sha: null}
    : {...state.pr, head: {...state.pr.head, sha: 'b'.repeat(40)}};
  await state.run(gate.jobs.approval);
  assert.equal(state.calls.some(([kind]) => kind === 'dispatch'), false);
});

test('an unlabeled snapshot cannot erase a label added while its reset was queued', async () => {
  const state = fixture();
  state.context.payload.action = 'synchronize';
  state.context.payload.pull_request.labels = [];
  await state.run(gate.jobs.approval);
  assert.deepEqual(state.calls, []);
  assert.equal(state.pr.labels[0].name, 'safe-to-test');
});

test('a queued reset preserves a re-applied label until its newer approval runs', async () => {
  const state = fixture();
  state.context.payload.action = 'synchronize';
  state.runs = [{...state.source, run_number: 101, status: 'queued'}];
  await state.run(gate.jobs.approval);
  assert.equal(state.pr.labels[0]?.name, 'safe-to-test');
  assert.equal(state.status('contract').state, 'pending');
  assert.equal(state.calls.some(([kind]) => kind === 'dispatch'), false);
  state.context.payload.action = 'labeled';
  state.context.runId = 101;
  state.context.runNumber = 101;
  await state.run(gate.jobs.approval);
  assert.equal(state.calls.filter(([kind]) => kind === 'dispatch').length, 1);
});

test('a newer approval for another PR does not preserve the old label', async () => {
  const state = fixture();
  state.context.payload.action = 'synchronize';
  state.runs = [{...state.source, run_number: 101, display_title: `approve-fork #999 ${sha} ready`}];
  await state.run(gate.jobs.approval);
  assert.deepEqual(state.pr.labels, []);
});

for (const action of ['labeled', 'synchronize']) {
  test(`a merge computed after ${action} times out is gated and its trusted workflow retried`, async () => {
    const state = fixture();
    state.context.payload.action = action;
    state.pr.mergeable = null;
    state.pr.merge_commit_sha = null;
    if (action === 'synchronize') state.source.display_title = `reset-fork #317 ${sha}`;
    await assert.rejects(state.run(gate.jobs.approval), /merge revision/);
    state.source.status = 'completed';
    state.source.conclusion = 'failure';
    state.pr.mergeable = true;
    state.pr.merge_commit_sha = 'd'.repeat(40);
    await state.run(reconciler.jobs.reconcile);
    assert.equal(state.status('contract', state.pr.merge_commit_sha).state, 'pending');
    assert.equal(state.status('contract', state.pr.merge_commit_sha).target_url, approvalURL);
    assert.deepEqual(state.calls.find(([kind]) => kind === 'rerun')[1], {...repo, run_id: 100});
    await state.run(gate.jobs.approval);
    const dispatch = state.calls.find(([kind]) => kind === 'dispatch');
    if (action === 'labeled') assert.equal(dispatch[1].inputs.merge_sha, state.pr.merge_commit_sha);
    else {
      assert.equal(dispatch, undefined);
      assert.deepEqual(state.pr.labels, []);
    }
  });
}

test('reconciliation does not replace existing merge results', async () => {
  const state = fixture();
  await state.run(worker.jobs.report);
  state.calls.length = 0;
  await state.run(reconciler.jobs.reconcile);
  assert.deepEqual(state.calls, []);
  assert.equal(state.status('contract', mergeSHA).state, 'success');
});

test('reconciliation gates a new base merge without reusing a successful old approval', async () => {
  const state = fixture();
  await state.run(worker.jobs.report);
  state.calls.length = 0;
  state.pr.merge_commit_sha = 'd'.repeat(40);
  state.source.status = 'completed';
  state.source.conclusion = 'success';
  await state.run(reconciler.jobs.reconcile);
  assert.equal(state.status('contract', state.pr.merge_commit_sha).state, 'pending');
  assert.equal(state.calls.some(([kind]) => kind === 'rerun'), false);
});

test('reconciliation leaves unresolved merge computation for the next scheduled run', async () => {
  const state = fixture();
  state.pr.mergeable = null;
  state.pr.merge_commit_sha = null;
  await state.run(reconciler.jobs.reconcile);
  assert.deepEqual(state.calls, []);
});

test('reconciliation cannot rerun an untrusted workflow', async () => {
  const state = fixture();
  state.source.path = '.github/workflows/fake.yaml';
  await assert.rejects(state.run(reconciler.jobs.reconcile), /trusted workflow/);
  assert.deepEqual(state.calls, []);
});

test('rerun API failure leaves the late merge gate pending for another retry', async () => {
  const state = fixture();
  state.pr.merge_commit_sha = 'd'.repeat(40);
  state.source.status = 'completed';
  state.source.conclusion = 'failure';
  state.rerunError = true;
  await assert.rejects(state.run(reconciler.jobs.reconcile), /rerun API error/);
  assert.equal(state.status('contract', state.pr.merge_commit_sha).state, 'pending');
  state.rerunError = false;
  state.calls.length = 0;
  await state.run(reconciler.jobs.reconcile);
  assert.deepEqual(state.calls, [['rerun', {...repo, run_id: 100}]]);
});

test('scheduled discovery only selects forks and Dependabot', async () => {
  const state = fixture();
  const internal = {...state.pr, number: 1, head: {...state.pr.head, repo: {full_name: `${repo.owner}/${repo.repo}`}}};
  state.prs = [state.pr, internal, {...internal, number: 2, user: {login: 'dependabot[bot]'}}];
  await state.run(reconciler.jobs.discover);
  assert.deepEqual(JSON.parse(state.outputs.prs), [317, 2]);
});

for (const action of ['synchronize', 'labeled']) {
  test(`out-of-order ${action} cannot overwrite a newer approval after reporting`, async () => {
    const state = fixture();
    await state.run(worker.jobs.report);
    state.calls.length = 0;
    state.context.payload.action = action;
    state.context.runId = 99;
    state.context.runNumber = 99;
    await state.run(gate.jobs.approval);
    assert.deepEqual(state.calls, []);
    assert.equal(state.status('contract').state, 'success');
    assert.equal(state.status('contract').target_url, approvalURL);
    assert.equal(state.pr.labels[0].name, 'safe-to-test');
  });
}

for (const failure of ['label', 'fork-approval', 'head-contract']) {
  test(`reset failure at ${failure} leaves the evaluated revision blocked`, async () => {
    const state = fixture();
    await state.run(worker.jobs.report);
    state.calls.length = 0;
    state.context.runId = 101;
    state.context.runNumber = 101;
    state.context.payload.action = 'reopened';
    if (failure === 'label') state.removeError = 403;
    else state.statusError = value => failure === 'head-contract'
      ? value.context === 'contract' && value.sha === sha
      : value.context === failure;
    await assert.rejects(state.run(gate.jobs.approval), /API error/);
    assert.equal(state.status('contract', mergeSHA).state, 'pending');
    assert.match(state.status('contract', mergeSHA).target_url, /101$/);
    // Even when the reset fails before label removal, an old reporter cannot restore success.
    state.statusError = undefined;
    state.calls.length = 0;
    await state.run(worker.jobs.report);
    assert.deepEqual(state.calls, []);
  });
}

test('initial invalidation failure propagates before label removal or dispatch', async () => {
  const state = fixture();
  state.context.payload.action = 'reopened';
  state.statusError = () => true;
  await assert.rejects(state.run(gate.jobs.approval), /API error/);
  assert.equal(state.calls.some(([kind]) => kind !== 'status'), false);
  assert.equal(state.pr.labels[0].name, 'safe-to-test');
});

test('a report API failure cannot restore the required gate early', async () => {
  const state = fixture();
  state.statusError = value => value.context === 'fork-approval';
  await assert.rejects(state.run(worker.jobs.report), /API error/);
  for (const revision of [sha, mergeSHA]) assert.equal(state.status('contract', revision).state, 'pending');
});

// Drift pins protect the trust boundary, not incidental workflow formatting.
test('internal PR checks use the merge revision evaluated by GitHub', () => {
  for (const name of ['checks.yaml', 'guardrails.yaml', 'mcp-protocol.yaml', 'e2e.yaml']) {
    for (const job of Object.values(read(name).jobs)) {
      for (const step of job.steps || []) {
        if (step.uses === 'actions/checkout@v4') {
          // The checkout default is github.sha: the synthetic merge commit for pull_request.
          assert.ok(step.with?.ref === undefined || step.with.ref === '${{ github.sha }}', name);
        }
      }
    }
  }
});

test('fork execution cannot inherit secrets, write tokens, caches, or reporter files', () => {
  assert.deepEqual(Object.keys(gate.on), ['pull_request_target']);
  assert.deepEqual(Object.keys(worker.on), ['workflow_dispatch']);
  assert.deepEqual(Object.keys(reconciler.on), ['schedule', 'workflow_dispatch']);
  assert.equal(worker['cache-mode'], 'none');
  assert.deepEqual(worker.permissions, {});
  for (const name of ['checks', 'e2e']) {
    const job = worker.jobs[name];
    assert.deepEqual(job.permissions, {contents: 'read'});
    assert.equal(job.needs, 'authorize');
    assert.doesNotMatch(JSON.stringify(job), /secrets\./);
    const checkout = job.steps.find(step => step.uses === 'actions/checkout@v4');
    assert.equal(checkout.with.ref, '${{ matrix.revision }}');
    assert.equal(job.strategy.matrix.revision, '${{ fromJSON(needs.authorize.outputs.revisions) }}');
    assert.equal(job.strategy['fail-fast'], false);
    assert.equal(checkout.with['persist-credentials'], false);
  }
  for (const job of [gate.jobs.approval, worker.jobs.authorize, worker.jobs.report, ...Object.values(reconciler.jobs)]) {
    assert.equal(job.steps.every(step => step.uses === 'actions/github-script@v7'), true);
  }
  assert.equal(gate.jobs.approval.concurrency.group, 'fork-status-${{ github.event.number }}');
  assert.equal(worker.jobs.report.concurrency.group, 'fork-status-${{ inputs.pr }}');
  assert.equal(reconciler.jobs.reconcile.concurrency.group, 'fork-status-${{ matrix.pr }}');
  // GitHub's default single pending slot would let a stale reporter cancel a queued reset.
  for (const job of [gate.jobs.approval, worker.jobs.report, reconciler.jobs.reconcile]) {
    assert.equal(job.concurrency.queue, 'max');
    assert.equal(job.concurrency['cancel-in-progress'], false);
  }
  assert.match(worker.jobs.checks.steps.at(-1).run, /check-repo-docs READY=1/);
});
