/**
 * Verify extension for Pi.
 *  - tool_call: screen bash/write/edit calls with `verify agent-hook` (exit 2 blocks).
 *  - agent_end: run `verify check --fast --json`; on failure notify the user and
 *    send a bounded follow-up message so the author repairs the findings.
 * Noise reduction only: `verify ship` re-checks everything.
 */
import { spawn } from "node:child_process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const MAX_FEEDBACK = 3; // consecutive failed checks fed back before giving up
const MAX_FINDINGS = 10;
const MAX_CHARS = 2000;
const MAX_OUTPUT = 64 * 1024;
const TIMEOUT_MS = 60_000;

function run(args: string[], cwd: string, stdin?: string): Promise<{ code: number; stdout: string; stderr: string }> {
	return new Promise((resolve) => {
		let stdout = "";
		let stderr = "";
		let settled = false;
		let timedOut = false;
		const child = spawn("verify", args, { cwd, detached: true, stdio: ["pipe", "pipe", "pipe"] });
		const timer = setTimeout(() => {
			timedOut = true;
			if (child.pid) {
				try { process.kill(-child.pid, "SIGKILL"); } catch { child.kill("SIGKILL"); }
			}
		}, TIMEOUT_MS);
		const done = (code: number) => {
			if (!settled) {
				settled = true;
				clearTimeout(timer);
				resolve({ code: timedOut ? -1 : code, stdout, stderr });
			}
		};
		child.stdout.on("data", (d: Buffer) => (stdout += d.toString().slice(0, Math.max(0, MAX_OUTPUT - stdout.length))));
		child.stderr.on("data", (d: Buffer) => (stderr += d.toString().slice(0, Math.max(0, MAX_OUTPUT - stderr.length))));
		child.on("error", () => done(-1));
		child.on("close", (code) => done(code ?? -1));
		child.stdin.on("error", () => {});
		child.stdin.end(stdin ?? "");
	});
}

export default function (pi: ExtensionAPI) {
	let feedback = 0;

	pi.on("tool_call", async (event, ctx) => {
		const payload = JSON.stringify({ toolName: event.toolName, input: event.input });
		const res = await run(["agent-hook"], ctx.cwd, payload);
		if (res.code === 2) {
			const reason = res.stderr.trim().split("\n")[0] || "blocked by verify agent-hook";
			return { block: true, reason };
		}
		return undefined; // verify missing or any other result: allow (noise reduction only)
	});

	pi.on("agent_end", async (_event, ctx) => {
		const res = await run(["check", "--fast", "--json"], ctx.cwd);
		if (res.code === 0) {
			feedback = 0;
			return;
		}
		let summary = "";
		try {
			const r = JSON.parse(res.stdout);
			const lines = (r.findings ?? [])
				.filter((f: any) => f.severity === "block")
				.slice(0, MAX_FINDINGS)
				.map((f: any) => `- ${f.id}${f.path ? ` (${f.path}${f.line ? `:${f.line}` : ""})` : ""}: ${f.message}`);
			summary = lines.join("\n") || r.message || "";
		} catch {
			summary = res.stdout || res.stderr;
		}
		summary = summary.slice(0, MAX_CHARS);
		if (ctx.hasUI) ctx.ui.notify("verify check --fast found problems", "warning");
		if (feedback >= MAX_FEEDBACK) return;
		feedback++;
		await pi.sendUserMessage(
			`verify check --fast failed. Fix these findings, then stop (do not bypass hooks):\n${summary}`,
			{ deliverAs: "followUp" },
		);
	});
}
