import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
export class AcpError extends Error {
    code;
    constructor(code, message) {
        super(message);
        this.name = "AcpError";
        this.code = code;
    }
}
export class PigoAcpClient {
    commandPath;
    args;
    cwd;
    env;
    events;
    child;
    lines;
    nextId = 1;
    pending = new Map();
    closed = false;
    constructor(options = {}) {
        this.commandPath = options.command ?? "pigo";
        this.args = options.args ?? ["--acp"];
        this.cwd = options.cwd;
        this.env = options.env;
        this.events = options.events ?? {};
    }
    start() {
        if (this.child !== undefined) {
            throw new Error("pigo-acp: client already started");
        }
        this.closed = false;
        this.child = spawn(this.commandPath, this.args, {
            cwd: this.cwd,
            env: this.env,
            stdio: ["pipe", "pipe", "pipe"],
            windowsHide: true,
        });
        this.lines = createInterface({ input: this.child.stdout });
        this.lines.on("line", (line) => this.handleLine(line));
        this.child.stderr.on("data", (chunk) => {
            for (const line of chunk.toString("utf8").split(/\r?\n/)) {
                if (line.trim() !== "") {
                    this.events.onStderr?.(line);
                }
            }
        });
        this.child.on("exit", (code, signal) => {
            this.closed = true;
            const error = new Error(`pigo-acp: process exited (code=${code}, signal=${signal})`);
            for (const pending of this.pending.values()) {
                pending.reject(error);
            }
            this.pending.clear();
            this.events.onExit?.(code, signal);
        });
        this.child.on("error", (error) => {
            this.closed = true;
            for (const pending of this.pending.values()) {
                pending.reject(error);
            }
            this.pending.clear();
        });
    }
    async initialize() {
        return this.request("initialize", {
            protocolVersion: 1,
            clientCapabilities: {},
            clientInfo: { name: "pigo-acp", version: "0.1.0" },
        });
    }
    async newSession(cwd, additionalDirectories = []) {
        return this.request("session/new", {
            cwd,
            mcpServers: [],
            ...(additionalDirectories.length > 0 ? { additionalDirectories } : {}),
        });
    }
    async loadSession(sessionId, cwd) {
        return this.request("session/load", { sessionId, cwd, mcpServers: [] });
    }
    async listSessions(cwd) {
        return this.request("session/list", { cwd });
    }
    async closeSession(sessionId) {
        await this.request("session/close", { sessionId });
    }
    async deleteSession(sessionId) {
        await this.request("session/delete", { sessionId });
    }
    async prompt(sessionId, text) {
        const result = await this.request("session/prompt", {
            sessionId,
            prompt: [{ type: "text", text }],
        });
        return result.stopReason;
    }
    cancel(sessionId) {
        this.notify("session/cancel", { sessionId });
    }
    async modelSet(sessionId, modelId) {
        await this.request("model/set", { sessionId, modelId });
    }
    async models() {
        return this.request("pigo/models", {});
    }
    async configGet() {
        return this.request("pigo/config", {});
    }
    async configSet(update) {
        return this.request("pigo/config", update);
    }
    async messages(sessionId, options = {}) {
        return this.request("pigo/messages", {
            sessionId,
            ...(options.before !== undefined ? { before: options.before } : {}),
            ...(options.limit !== undefined ? { limit: options.limit } : {}),
        });
    }
    async command(sessionId, command) {
        const result = await this.request("pigo/command", {
            sessionId,
            command,
        });
        return result.text;
    }
    respondPermission(requestId, outcome, optionId) {
        const result = outcome === "cancelled"
            ? { outcome: { outcome: "cancelled" } }
            : { outcome: { outcome: "selected", optionId } };
        this.write({ jsonrpc: "2.0", id: requestId, result });
    }
    close() {
        if (this.closed) {
            return;
        }
        this.closed = true;
        this.child?.stdin.end();
        this.lines?.close();
        this.child?.kill();
    }
    async request(method, params) {
        if (this.closed || this.child === undefined) {
            throw new Error("pigo-acp: client is not running");
        }
        const id = this.nextId++;
        return new Promise((resolve, reject) => {
            this.pending.set(id, { resolve: resolve, reject });
            this.write({ jsonrpc: "2.0", id, method, params });
        });
    }
    notify(method, params) {
        this.write({ jsonrpc: "2.0", method, params });
    }
    write(envelope) {
        if (this.child === undefined || this.closed) {
            throw new Error("pigo-acp: client is not running");
        }
        this.child.stdin.write(`${JSON.stringify(envelope)}\n`);
    }
    handleLine(line) {
        let envelope;
        try {
            envelope = JSON.parse(line);
        }
        catch {
            return;
        }
        if (envelope.id !== undefined && envelope.method !== undefined) {
            this.handleIncomingRequest(envelope);
            return;
        }
        if (envelope.id !== undefined) {
            this.handleResponse(envelope);
            return;
        }
        this.handleNotification(envelope);
    }
    handleIncomingRequest(envelope) {
        if (envelope.method === "session/request_permission") {
            const params = (envelope.params ?? {});
            this.events.onPermission?.({
                requestId: envelope.id,
                sessionId: params.sessionId,
                toolCall: params.toolCall,
                options: params.options,
            });
            return;
        }
        this.write({
            jsonrpc: "2.0",
            id: envelope.id,
            error: { code: -32601, message: `method not found: ${envelope.method}` },
        });
    }
    handleResponse(envelope) {
        const pending = this.pending.get(envelope.id);
        if (pending === undefined) {
            return;
        }
        this.pending.delete(envelope.id);
        if (envelope.error !== undefined) {
            pending.reject(new AcpError(envelope.error.code, envelope.error.message));
            return;
        }
        pending.resolve(envelope.result);
    }
    handleNotification(envelope) {
        if (envelope.method === "session/update") {
            const params = (envelope.params ?? {});
            this.events.onUpdate?.(params.sessionId, params.update);
            return;
        }
        if (envelope.method === "pigo/event") {
            const params = (envelope.params ?? {});
            this.events.onEvent?.(params.sessionId, params.event);
        }
    }
}
