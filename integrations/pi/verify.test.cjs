// Run with PI_PACKAGE=/path/to/installed/@earendil-works/pi-coding-agent node --test integrations/pi/verify.test.cjs
// Registers the real extension through the installed jiti loader, and runs a scratch Verify executable.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { mkdtempSync, mkdirSync, writeFileSync, chmodSync, rmSync } = require('node:fs');
const { tmpdir } = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const pkg = process.env.PI_PACKAGE;

test('registered tool_call and agent_end feed bounded real Verify feedback', { skip: !pkg && 'set PI_PACKAGE to installed Pi SDK' }, async () => {
  const root = mkdtempSync(path.join(tmpdir(), 'verify-pi-'));
  try {
    const bin = path.join(root, 'bin');
    const repo = path.join(root, 'repo');
    mkdirSync(bin);
    mkdirSync(repo);
    execFileSync('go', ['build', '-o', path.join(bin, 'verify'), './cmd/verify'], { cwd: path.resolve(__dirname, '../..') });
    const prek = path.join(bin, 'prek');
    writeFileSync(prek, '#!/bin/sh\necho "fixture hook failed" >&2\nexit 1\n');
    chmodSync(prek, 0o755);
    const git = (...args) => execFileSync('git', args, { cwd: repo, env: { ...process.env, GIT_AUTHOR_NAME: 't', GIT_AUTHOR_EMAIL: 't@e', GIT_COMMITTER_NAME: 't', GIT_COMMITTER_EMAIL: 't@e' } });
    git('init', '-q', '-b', 'main');
    writeFileSync(path.join(repo, '.pre-commit-config.yaml'), 'repos: []\n');
    writeFileSync(path.join(repo, 'README.md'), 'base\n');
    git('add', '.');
    git('commit', '-q', '-m', 'base');
    git('checkout', '-q', '-b', 'feature');
    writeFileSync(path.join(repo, 'README.md'), 'changed\n');
    const originalPath = process.env.PATH;
    process.env.PATH = `${bin}${path.delimiter}${originalPath}`;
    try {
      const { createJiti } = require(path.join(pkg, 'node_modules/jiti'));
      const factory = createJiti(__filename)(path.join(__dirname, 'verify.ts')).default;
      const handlers = {};
      const sent = [];
      const notices = [];
      factory({
        on(name, fn) { handlers[name] = fn; },
        async sendUserMessage(message, options) { sent.push({ message, options }); },
      });
      assert.equal(typeof handlers.tool_call, 'function');
      assert.equal(typeof handlers.agent_end, 'function');
      const ctx = { cwd: repo, hasUI: true, ui: { notify: (...args) => notices.push(args) } };
      const block = await handlers.tool_call({ toolName: 'bash', input: { command: 'git commit --no-verify' } }, ctx);
      assert.equal(block.block, true);
      assert.match(block.reason, /no-verify/);
      assert.equal(await handlers.tool_call({ toolName: 'bash', input: { command: 'git status' } }, ctx), undefined);
      for (let i = 0; i < 5; i++) await handlers.agent_end({}, ctx);
      assert.equal(sent.length, 3, 'feedback capped despite repeated failures');
      assert.equal(notices.length, 5);
      assert.match(sent[0].message, /verify check --fast failed/);
      assert.match(sent[0].message, /prek-pre-commit|failed/);
      assert.deepEqual(sent[0].options, { deliverAs: 'followUp' });
      assert.ok(sent.every(({ message }) => message.length <= 2200));
      // A clean check resets the counter. A fresh failure may be reported again.
      writeFileSync(prek, '#!/bin/sh\nexit 0\n');
      await handlers.agent_end({}, ctx);
      writeFileSync(prek, '#!/bin/sh\nexit 1\n');
      await handlers.agent_end({}, ctx);
      assert.equal(sent.length, 4);
    } finally {
      process.env.PATH = originalPath;
    }
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
