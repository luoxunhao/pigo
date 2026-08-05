import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface, type Interface } from "node:readline";

import type {
  AcpClientEvents,
  AcpClientOptions,
  AcpPermissionRequest,
  InitializeResult,
  ListSessionsResult,
  LoadSessionResult,
  NewSessionResult,
  PigoConfigResult,
  PigoConfigUpdate,
  PigoMessagesResult,
  PigoModelsResult,
  PermissionOptionId,
} from "./types.js";

interface RpcEnvelope {
  jsonrpc: "2.0";
  id?: number | string;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { code: number; message: string; data?: unknown };
}

interface PendingRequest {
  resolve: (value: unknown) => void;
  reject: (reason: Error) => void;
}

export class AcpError extends Error {
  readonly code: number;

  constructor(code: number, message: string) {
    super(message);
    this.name = "AcpError";
    this.code = code;
  }
}

export class PigoAcpClient {
  private readonly commandPath: string;
  private readonly args: string[];
  private readonly cwd: string | undefined;
  private readonly env: NodeJS.ProcessEnv | undefined;
  private readonly events: AcpClientEvents;
  private child: ChildProcessWithoutNullStreams | undefined;
  private lines: Interface | undefined;
  private nextId = 1;
  private readonly pending = new Map<number, PendingRequest>();
  private closed = false;

  constructor(options: AcpClientOptions = {}) {
    this.commandPath = options.command ?? "pigo";
    this.args = options.args ?? ["--acp"];
    this.cwd = options.cwd;
    this.env = options.env;
    this.events = options.events ?? {};
  }

  start(): void {
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
    this.child.stderr.on("data", (chunk: Buffer) => {
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

  async initialize(): Promise<InitializeResult> {
    return this.request<InitializeResult>("initialize", {
      protocolVersion: 1,
      clientCapabilities: {},
      clientInfo: { name: "pigo-acp", version: "0.1.0" },
    });
  }

  async newSession(cwd: string, additionalDirectories: string[] = []): Promise<NewSessionResult> {
    return this.request<NewSessionResult>("session/new", {
      cwd,
      mcpServers: [],
      ...(additionalDirectories.length > 0 ? { additionalDirectories } : {}),
    });
  }

  async loadSession(sessionId: string, cwd: string): Promise<LoadSessionResult> {
    return this.request<LoadSessionResult>("session/load", { sessionId, cwd, mcpServers: [] });
  }

  async listSessions(cwd: string): Promise<ListSessionsResult> {
    return this.request<ListSessionsResult>("session/list", { cwd });
  }

  async closeSession(sessionId: string): Promise<void> {
    await this.request<Record<string, never>>("session/close", { sessionId });
  }

  async deleteSession(sessionId: string): Promise<void> {
    await this.request<Record<string, never>>("session/delete", { sessionId });
  }

  async prompt(sessionId: string, text: string): Promise<string> {
    const result = await this.request<{ stopReason: string }>("session/prompt", {
      sessionId,
      prompt: [{ type: "text", text }],
    });
    return result.stopReason;
  }

  cancel(sessionId: string): void {
    this.notify("session/cancel", { sessionId });
  }

  async modelSet(sessionId: string, modelId: string): Promise<void> {
    await this.request<Record<string, never>>("model/set", { sessionId, modelId });
  }

  async models(): Promise<PigoModelsResult> {
    return this.request<PigoModelsResult>("pigo/models", {});
  }

  async configGet(): Promise<PigoConfigResult> {
    return this.request<PigoConfigResult>("pigo/config", {});
  }

  async configSet(update: PigoConfigUpdate): Promise<PigoConfigResult> {
    return this.request<PigoConfigResult>("pigo/config", update);
  }

  async messages(
    sessionId: string,
    options: { before?: string; limit?: number } = {},
  ): Promise<PigoMessagesResult> {
    return this.request<PigoMessagesResult>("pigo/messages", {
      sessionId,
      ...(options.before !== undefined ? { before: options.before } : {}),
      ...(options.limit !== undefined ? { limit: options.limit } : {}),
    });
  }

  async command(sessionId: string, command: string): Promise<string> {
    const result = await this.request<{ text: string }>("pigo/command", {
      sessionId,
      command,
    });
    return result.text;
  }

  respondPermission(requestId: number | string, outcome: "selected" | "cancelled", optionId?: PermissionOptionId): void {
    const result =
      outcome === "cancelled"
        ? { outcome: { outcome: "cancelled" } }
        : { outcome: { outcome: "selected", optionId } };
    this.write({ jsonrpc: "2.0", id: requestId, result });
  }

  close(): void {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.child?.stdin.end();
    this.lines?.close();
    this.child?.kill();
  }

  private async request<T>(method: string, params: unknown): Promise<T> {
    if (this.closed || this.child === undefined) {
      throw new Error("pigo-acp: client is not running");
    }
    const id = this.nextId++;
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: resolve as (value: unknown) => void, reject });
      this.write({ jsonrpc: "2.0", id, method, params });
    });
  }

  private notify(method: string, params: unknown): void {
    this.write({ jsonrpc: "2.0", method, params });
  }

  private write(envelope: RpcEnvelope): void {
    if (this.child === undefined || this.closed) {
      throw new Error("pigo-acp: client is not running");
    }
    this.child.stdin.write(`${JSON.stringify(envelope)}\n`);
  }

  private handleLine(line: string): void {
    let envelope: RpcEnvelope;
    try {
      envelope = JSON.parse(line) as RpcEnvelope;
    } catch {
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

  private handleIncomingRequest(envelope: RpcEnvelope): void {
    if (envelope.method === "session/request_permission") {
      const params = (envelope.params ?? {}) as {
        sessionId: string;
        toolCall: AcpPermissionRequest["toolCall"];
        options: AcpPermissionRequest["options"];
      };
      this.events.onPermission?.({
        requestId: envelope.id as number | string,
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

  private handleResponse(envelope: RpcEnvelope): void {
    const pending = this.pending.get(envelope.id as number);
    if (pending === undefined) {
      return;
    }
    this.pending.delete(envelope.id as number);
    if (envelope.error !== undefined) {
      pending.reject(new AcpError(envelope.error.code, envelope.error.message));
      return;
    }
    pending.resolve(envelope.result);
  }

  private handleNotification(envelope: RpcEnvelope): void {
    if (envelope.method === "session/update") {
      const params = (envelope.params ?? {}) as { sessionId: string; update: Record<string, unknown> };
      this.events.onUpdate?.(params.sessionId, params.update);
      return;
    }
    if (envelope.method === "pigo/event") {
      const params = (envelope.params ?? {}) as { sessionId: string; event: Record<string, unknown> };
      this.events.onEvent?.(params.sessionId, params.event);
    }
  }
}
