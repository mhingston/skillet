import { mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const cache = process.env.SKILLET_PI_CACHE ?? join(homedir(), ".pi", "agent", "skillet");
const helper = process.env.SKILLET_ADAPTER_BIN ?? "skillet-adapter";
const server = process.env.SKILLET_MCP_URL ?? "http://localhost:8080/mcp";
const active = new Set<string>();

async function run(pi: ExtensionAPI, args: string[]): Promise<any> {
	const result = await pi.exec(helper, args, { timeout: 120_000 });
	if (result.code !== 0) throw new Error(result.stderr || `skillet-adapter exited ${result.code}`);
	return JSON.parse(result.stdout);
}

export default function (pi: ExtensionAPI) {
	pi.on("resources_discover", () => ({ skillPaths: [...active] }));
	pi.registerCommand("skillet", {
		description: "Search or activate verified skills from Skillet",
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
				const out = await run(pi, ["materialize", "-server", server, "-candidate", words[0], "-destination", cache, "-harness", "pi"]);
				active.add(out.entrypoint);
				await ctx.reload();
				ctx.ui.notify(`Activated ${out.skill.name}`, "info");
				return;
			}
			if (action === "deactivate" && words.length) {
				for (const path of active) if (path.endsWith(`/${words[0]}/SKILL.md`)) active.delete(path);
				await ctx.reload();
				ctx.ui.notify(`Deactivated ${words[0]}`, "info");
				return;
			}
			ctx.ui.notify("Usage: /skillet search <query> | /skillet activate <candidate-id> | /skillet deactivate <skill>", "warning");
		},
	});
}
