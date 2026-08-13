import assert from "node:assert/strict";
import test from "node:test";

import { PigoAcpClient } from "../dist/index.js";

const mockServer = `
import readline from "node:readline";
const rl = readline.createInterface({ input: process.stdin });
rl.on("line", (line) => {
  const msg = JSON.parse(line);
  if (msg.method === "initialize") {
    const tree = msg.params?.clientCapabilities?._meta?.pigo?.sessionTree;
    if (!tree || tree.version !== 1) {
      console.error("missing sessionTree v1 capability");
      process.exit(2);
    }
    console.log(JSON.stringify({
      jsonrpc: "2.0",
      id: msg.id,
      result: {
        protocolVersion: 1,
        agentCapabilities: {
          loadSession: true,
          promptCapabilities: {},
          sessionCapabilities: {},
          _meta: { pigo: { sessionTree: { version: 1 } } },
        },
        authMethods: [],
        agentInfo: { name: "mock", version: "0.0.0" },
      },
    }));
    setTimeout(() => {
      console.log(JSON.stringify({
        jsonrpc: "2.0",
        method: "session/update",
        params: {
          sessionId: "s1",
          update: {
            sessionUpdate: "session_info_update",
            _meta: {
              pigo: {
                sessionTree: {
                  version: 1,
                  currentLeafId: "e2",
                  currentLane: "main",
                  lanes: [{ lane: "main", leafId: "e2" }],
                },
              },
            },
          },
        },
      }));
    }, 20);
    return;
  }
  console.log(JSON.stringify({ jsonrpc: "2.0", id: msg.id, result: {} }));
});
`;

test("initialize declares tree v1 and consumes session_info_update", async () => {
  let info;
  const client = new PigoAcpClient({
    command: process.execPath,
    args: ["-e", mockServer],
    events: {
      onSessionInfo: (_sessionId, update) => {
        info = update;
      },
    },
  });
  client.start();
  try {
    const init = await client.initialize();
    assert.equal(init.agentCapabilities?._meta?.pigo?.sessionTree?.version, 1);
    assert.equal(client.sessionTreeCapability()?.version, 1);
    await new Promise((resolve) => setTimeout(resolve, 60));
    assert.equal(info?.currentLeafId, "e2");
    assert.equal(info?.currentLane, "main");
    assert.deepEqual(info?.lanes, [{ lane: "main", leafId: "e2" }]);
  } finally {
    client.close();
  }
});
