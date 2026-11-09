// Installed by glab minder setup. Remove it with: glab minder setup --uninstall
//
// Syncs each OpenCode session to GitLab when it goes idle. glab is started
// detached, because OpenCode does not wait for plugins when it exits.
// OpenCode 1 calls server, and OpenCode 2 calls setup.
import { spawn } from "node:child_process"

function sync(directory, sessionID) {
  if (!sessionID) return
  // glab checks for a Claude Code session first, which OpenCode inherits when
  // it is started from inside Claude Code.
  const env = { ...process.env, OPENCODE_SESSION_ID: sessionID }
  delete env.CLAUDE_CODE_SESSION_ID
  try {
    const child = spawn("glab", ["minder", "audit", "sync", "--silent"], {
      cwd: directory,
      env,
      detached: true,
      stdio: "ignore",
      windowsHide: true,
    })
    child.on("error", () => {})
    child.unref()
  } catch {
    // A missing glab must not break OpenCode. glab minder doctor reports it.
  }
}

// OpenCode 2 ends every turn with one of these events.
const turnEnded = new Set(["session.execution.succeeded", "session.execution.failed", "session.execution.interrupted"])

export default {
  id: "glab-govern",
  server: async ({ directory }) => ({
    event: async ({ event }) => {
      if (event.type === "session.idle") sync(directory, event.properties?.sessionID)
    },
  }),
  setup: async (ctx) => {
    const controller = new AbortController()
    const watch = async () => {
      for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
        if (turnEnded.has(event?.type)) {
          sync(event.location?.directory ?? ctx.location?.directory, event.data?.sessionID)
        }
      }
    }
    watch().catch(() => {})
    return () => controller.abort()
  },
}
