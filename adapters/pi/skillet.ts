import { randomUUID } from "node:crypto";
import { mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const cache = process.env.SKILLET_PI_CACHE ?? join(homedir(), ".pi", "agent", "skillet");
const helper = process.env.SKILLET_ADAPTER_BIN ?? "skillet-adapter";
const server = process.env.SKILLET_MCP_URL ?? "http://localhost:8080/mcp";
const active = new Map<string, any>();
const correlation = randomUUID();

async function run(pi: ExtensionAPI, args: string[]): Promise<any> {
	const result = await pi.exec(helper, args, { timeout: 120_000 });
	if (result.code !== 0) throw new Error(result.stderr || `skillet-adapter exited ${result.code}`);
	return JSON.parse(result.stdout);
}

async function report(pi: ExtensionAPI, lifecycle: any, event: "activated" | "deactivated"): Promise<void> {
	if (!lifecycle?.revision_id) return;
	await run(pi, [
		"lifecycle", "-server", server,
		"-reference", JSON.stringify(lifecycle),
		"-event", event,
		"-source", "pi",
		"-correlation", correlation,
	]);
}

async function submitFeedback(pi: ExtensionAPI, lifecycle: any, category: string, summary: string): Promise<void> {
	if (!lifecycle?.revision_id) throw new Error("active skill has no Skillet provenance");
	await run(pi, [
		"feedback", "-server", server,
		"-reference", JSON.stringify(lifecycle),
		"-category", category,
		"-summary", summary,
		"-source", "pi",
		"-correlation", correlation,
	]);
}

export default function (pi: ExtensionAPI) {
	pi.on("resources_discover", () => ({ skillPaths: [...active.keys()] }));
	pi.registerCommand("skillet", {
		description: "Search, activate, deactivate, or report feedback for verified Skillet skills",
		handler: async (args, ctx) => {
			const words = args.trim().split(/\s+/).filter(Boolean);
			const action = words.shift();
			if (action === "search" && words.length) {
				const out = await run(pi, ["search", "-server", server, "-query", words.join(" "), "-limit", "5"]);
				ctx.ui.notify(out.candidates?.map((c: any) => `${c.candidate_id}: ${c.skill.description}`).join("\n") || "No candidates", "info");
				return;
			}
			if (action === "activate" && words.length) {
				mkdirSync(cache, { recursive: true });
				const selector = words[0];
				const flag = selector.includes("@") ? ["-skill-id", selector.slice(0, selector.indexOf("@")), ...(selector.slice(selector.indexOf("@") + 1).startsWith("^") ? ["-range", selector.slice(selector.indexOf("@") + 1)] : ["-version", selector.slice(selector.indexOf("@") + 1)])] : ["-candidate", selector];
				const out = await run(pi, ["materialize", "-server", server, ...flag, "-destination", cache, "-harness", "pi"]);
				active.set(out.entrypoint, out.lifecycle);
				await ctx.reload();
				try {
					await report(pi, out.lifecycle, "activated");
				} catch (error: any) {
					ctx.ui.notify(`Activated ${out.skill.name}; lifecycle telemetry failed: ${error?.message ?? error}`, "warning");
					return;
				}
				ctx.ui.notify(`Activated ${out.skill.name}`, "info");
				return;
			}
			if (action === "deactivate" && words.length) {
				let lifecycle: any;
				for (const [path, reference] of active) {
					if (path.endsWith(`/${words[0]}/SKILL.md`)) {
						lifecycle = reference;
						active.delete(path);
					}
				}
				await ctx.reload();
				if (lifecycle) {
					try {
						await report(pi, lifecycle, "deactivated");
					} catch (error: any) {
						ctx.ui.notify(`Deactivated ${words[0]}; lifecycle telemetry failed: ${error?.message ?? error}`, "warning");
						return;
					}
				}
				ctx.ui.notify(`Deactivated ${words[0]}`, "info");
				return;
			}
			if (action === "feedback" && words.length >= 3) {
				const skill = words.shift()!;
				const category = words.shift()!;
				const summary = words.join(" ");
				let lifecycle: any;
				for (const [path, reference] of active) {
					if (path.endsWith(`/${skill}/SKILL.md`)) {
						lifecycle = reference;
						break;
					}
				}
				if (!lifecycle) {
					ctx.ui.notify(`No active Skillet skill named ${skill}`, "warning");
					return;
				}
				try {
					await submitFeedback(pi, lifecycle, category, summary);
					ctx.ui.notify(`Recorded feedback for ${skill}`, "info");
				} catch (error: any) {
					ctx.ui.notify(`Feedback failed: ${error?.message ?? error}`, "warning");
				}
				return;
			}
			ctx.ui.notify("Usage: /skillet search <query> | /skillet activate <candidate-id or skill-id@version/range> | /skillet deactivate <skill> | /skillet feedback <skill> <category> <summary>", "warning");
		},
	});
}
