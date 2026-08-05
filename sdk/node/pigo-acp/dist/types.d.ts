export type PermissionOptionId = "allow_once" | "allow_always" | "reject_once" | "reject_always";
export interface AcpSessionSummary {
    sessionId: string;
    title: string;
    cwd: string;
    model: string;
    createdAt: string;
    updatedAt: string;
    messageCount: number;
    toolCallCount: number;
    parentSessionId?: string;
}
export type AcpContentBlock = {
    type: "text";
    text: string;
} | {
    type: "thinking";
    thinking: string;
} | {
    type: "toolCall";
    id: string;
    name: string;
    arguments: unknown;
} | {
    type: "image";
    data: string;
    mimeType: string;
};
export interface AcpMessage {
    id: string;
    parentId?: string;
    role: "user" | "assistant" | "toolResult" | "compaction" | string;
    timestamp: string;
    content: AcpContentBlock[];
    model?: string;
    stopReason?: string;
    toolCallId?: string;
    toolName?: string;
    isError?: boolean;
}
export interface AcpPermissionOption {
    optionId: PermissionOptionId;
    name: string;
    kind: PermissionOptionId;
}
export interface AcpPermissionRequest {
    requestId: number | string;
    sessionId: string;
    toolCall: {
        toolCallId: string;
        title: string;
        kind?: string;
        status?: string;
        rawInput?: unknown;
    };
    options: AcpPermissionOption[];
}
export interface InitializeResult {
    protocolVersion: number;
    agentCapabilities: {
        loadSession: boolean;
        promptCapabilities: Record<string, unknown>;
        sessionCapabilities: Record<string, unknown>;
        _meta?: Record<string, unknown>;
    };
    authMethods: unknown[];
    agentInfo: {
        name: string;
        version: string;
    };
}
export interface NewSessionResult {
    sessionId: string;
    configOptions: unknown[];
    models: {
        currentModelId: string;
        availableModels: unknown[];
    };
}
export interface LoadSessionResult extends NewSessionResult {
    messages: AcpMessage[];
}
export interface ListSessionsResult {
    sessions: AcpSessionSummary[];
    nextCursor: unknown;
}
export interface PigoModelEntry {
    provider: string;
    modelId: string;
    displayName: string;
}
export interface PigoModelsResult {
    currentModelId: string;
    models: PigoModelEntry[];
}
export interface PigoConfigResult {
    model: string;
    baseUrl: string;
    protocol: string;
    provider: string;
    thinkingLevel: string;
    apiKeyConfigured: boolean;
    configPath: string;
    needsRestart: boolean;
}
export interface PigoConfigUpdate {
    model?: string;
    baseUrl?: string;
    apiKey?: string;
    protocol?: string;
    provider?: string;
    thinkingLevel?: string;
}
export interface PigoMessagesResult {
    messages: AcpMessage[];
    hasMore: boolean;
    nextCursor: unknown;
}
export interface AcpClientEvents {
    onUpdate?: (sessionId: string, update: Record<string, unknown>) => void;
    onEvent?: (sessionId: string, event: Record<string, unknown>) => void;
    onPermission?: (request: AcpPermissionRequest) => void;
    onStderr?: (line: string) => void;
    onExit?: (code: number | null, signal: NodeJS.Signals | null) => void;
}
export interface AcpClientOptions {
    command?: string;
    args?: string[];
    cwd?: string;
    env?: NodeJS.ProcessEnv;
    events?: AcpClientEvents;
}
