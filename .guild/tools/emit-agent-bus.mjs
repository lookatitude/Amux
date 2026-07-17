import fs from "node:fs";
import path from "node:path";

const [runDir, event, laneId, taskId, detail = ""] = process.argv.slice(2);
if (!runDir || !event || !taskId) {
  throw new Error("usage: emit-agent-bus.mjs <runDir> <event> <laneId> <taskId> [detail]");
}

const guildDir = path.resolve(runDir, "..", "..");
const lockPath = path.join(guildDir, ".lock");
const busDir = path.join(runDir, "agent-bus");
const target = path.join(busDir, "events.ndjson");
fs.mkdirSync(busDir, { recursive: true });

let lock;
try {
  lock = fs.openSync(lockPath, "wx");
  const current = fs.existsSync(target) ? fs.readFileSync(target, "utf8") : "";
  const record = {
    schema_version: "guild.agent_bus_event.v1",
    ts: new Date().toISOString(),
    run_id: path.basename(runDir),
    event,
    lane_id: laneId,
    task_id: taskId,
    ...(detail ? { detail } : {}),
  };
  const temp = `${target}.${process.pid}.tmp`;
  fs.writeFileSync(temp, `${current}${JSON.stringify(record)}\n`, "utf8");
  fs.renameSync(temp, target);
  process.stdout.write(`${target}\n`);
} finally {
  if (lock !== undefined) fs.closeSync(lock);
  try { fs.unlinkSync(lockPath); } catch {}
}
