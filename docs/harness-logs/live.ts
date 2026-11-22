/** Normalized extensions emitted by the Go LiveParser API for owned subprocesses. */
export interface ILiveUsage {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  reasoning: number;
  total: number;
}

export interface ILiveResult {
  text: string;
  hasText: boolean;
  structuredOutput?: unknown;
  model?: string;
  sessionId?: string;
  usage?: ILiveUsage;
  isError: boolean;
  errorMessage?: string;
  nativeSubtype?: string;
}

/** Extensions to the existing normalized EventData contract. */
export interface ILiveEventData {
  delta?: string;
  result?: ILiveResult;
  native?: unknown;
  [key: string]: unknown;
}
