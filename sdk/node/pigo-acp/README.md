# pigo-acp

Node.js Agent Client Protocol client for [pigo](https://github.com/smallnest/pigo).

```ts
import { PigoAcpClient } from "pigo-acp";

const client = new PigoAcpClient({
  command: "pigo",
  args: ["--acp"],
  events: {
    onUpdate: (sessionId, update) => console.log(sessionId, update),
    onPermission: async (request) => {
      client.respondPermission(request.requestId, "selected", "allow_once");
    },
  },
});
client.start();

await client.initialize();
const { sessionId } = await client.newSession(process.cwd());
const stopReason = await client.prompt(sessionId, "hello");
```

## Development

```bash
npm install
npm run build
```
