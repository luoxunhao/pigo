import { PigoAcpClient } from "../dist/index.js";

const client = new PigoAcpClient({
  command: process.env.PIGO_ACP_COMMAND ?? "pigo",
  args: ["acp"],
  cwd: process.cwd(),
  events: {
    onUpdate: (_sessionId, update) => {
      if (update.sessionUpdate === "agent_message_chunk") {
        const text = update.content?.text;
        if (typeof text === "string") {
          process.stdout.write(text);
        }
      }
    },
    onStderr: (line) => console.error("pigo:", line),
  },
});

client.start();
await client.initialize();

const { sessionId } = await client.newSession(process.cwd());
console.log(`\n[smoke] session: ${sessionId}`);

const stopReason = await client.prompt(sessionId, process.env.PIGO_ACP_PROMPT ?? "只回复OK");
console.log(`\n[smoke] stopReason: ${stopReason}`);

const sessions = await client.listSessions(process.cwd());
console.log(`[smoke] listSessions: ${sessions.sessions.length}`);

const loaded = await client.loadSession(sessionId, process.cwd());
console.log(`[smoke] loadMessages: ${loaded.messages.length}`);

client.close();
