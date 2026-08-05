import type { AcpClientOptions, InitializeResult, ListSessionsResult, LoadSessionResult, NewSessionResult, PigoConfigResult, PigoConfigUpdate, PigoMessagesResult, PigoModelsResult, PermissionOptionId } from "./types.js";
export declare class AcpError extends Error {
    readonly code: number;
    constructor(code: number, message: string);
}
export declare class PigoAcpClient {
    private readonly commandPath;
    private readonly args;
    private readonly cwd;
    private readonly env;
    private readonly events;
    private child;
    private lines;
    private nextId;
    private readonly pending;
    private closed;
    constructor(options?: AcpClientOptions);
    start(): void;
    initialize(): Promise<InitializeResult>;
    newSession(cwd: string, additionalDirectories?: string[]): Promise<NewSessionResult>;
    loadSession(sessionId: string, cwd: string): Promise<LoadSessionResult>;
    listSessions(cwd: string): Promise<ListSessionsResult>;
    closeSession(sessionId: string): Promise<void>;
    deleteSession(sessionId: string): Promise<void>;
    prompt(sessionId: string, text: string): Promise<string>;
    cancel(sessionId: string): void;
    modelSet(sessionId: string, modelId: string): Promise<void>;
    models(): Promise<PigoModelsResult>;
    configGet(): Promise<PigoConfigResult>;
    configSet(update: PigoConfigUpdate): Promise<PigoConfigResult>;
    messages(sessionId: string, options?: {
        before?: string;
        limit?: number;
    }): Promise<PigoMessagesResult>;
    command(sessionId: string, command: string): Promise<string>;
    respondPermission(requestId: number | string, outcome: "selected" | "cancelled", optionId?: PermissionOptionId): void;
    close(): void;
    private request;
    private notify;
    private write;
    private handleLine;
    private handleIncomingRequest;
    private handleResponse;
    private handleNotification;
}
