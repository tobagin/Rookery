export type UnitState = "failed" | "running" | "pending" | "stopped" | "unknown";

export interface AuthState {
  required: boolean;
  authenticated: boolean;
  readOnly: boolean;
  setupNeeded: boolean;
  onboarding: boolean;
  username: string;
  email: string;
  role: string;
  oidc: { enabled?: boolean; name?: string } | null;
  passwordLogin: boolean;
}

export interface Unit {
  scope: string;
  name: string;
  kind: string;
  description?: string;
  image?: string;
  active?: string;
  sub?: string;
  load?: string;
  result?: string;
  exitCode?: number;
  restarts?: number;
  pod?: string;
  gpus?: string[];
  service?: string;
  path?: string;
  unitFile?: string;
  readOnly?: boolean;
}

export interface HostInfo {
  metrics?: {
    hostname?: string;
    kernel?: string;
    cpuPct?: number;
    load1?: number;
    cores?: number;
    memTotalKb?: number;
    memAvailKb?: number;
  };
  podman?: { version?: string };
  selinuxEnforcing?: boolean;
  generatorAvailable?: boolean;
  scopes?: string[];
}

export interface LicenseStatus {
  edition: string;
  plan: string;
  managedNodes: number;
  nodeLimit: number;
  nodes: string[];
  enterpriseAvailable: boolean;
  enforcement: string;
  message: string;
}

export interface ManagedNode {
  id: string;
  address?: string;
  local: boolean;
  scopes: Array<{ label: string; user?: string; system: boolean }>;
  labels?: string[];
  unitDirs: string[];
  units: number;
  running: number;
  failed: number;
  unknown: number;
  errors?: string[];
}

export interface NodeGroup {
  label: string;
  nodes: string[];
  units: number;
  running: number;
  failed: number;
  unknown: number;
}

export interface PolicyFinding {
  key: string;
  policy: string;
  severity: string;
  node: string;
  scope: string;
  unit?: string;
  message: string;
  waived?: boolean;
  waiverReason?: string;
  waivedBy?: string;
}

export interface AuditEvent {
  id: number;
  actor: string;
  action: string;
  target: string;
  detail?: unknown;
  createdAt?: string;
}

export interface ValidationResult {
  available?: boolean;
  valid?: boolean;
  output?: string;
}

export interface UpdateInfo {
  scope: string;
  name: string;
  image?: string;
  updateAvailable?: boolean;
  note?: string;
}

export interface GPUDevice {
  host?: string;
  vendor: string;
  name: string;
  utilizationPct: number;
  memoryUsedMb: number;
  memoryTotalMb: number;
}

export function stateClass(unit: Unit): UnitState {
  if (unit.active === "failed") return "failed";
  if (unit.active === "active") return "running";
  if (unit.active === "activating" || unit.active === "deactivating") return "pending";
  if (unit.load === "unknown") return "unknown";
  return "stopped";
}

export function stateLabel(unit: Unit): string {
  if (unit.load === "unknown") return "unknown";
  let label = unit.sub && unit.sub !== unit.active ? `${unit.active} (${unit.sub})` : unit.active || "unknown";
  if (unit.result === "exit-code") label += ` · exit ${unit.exitCode ?? "?"}`;
  return label;
}

export function fmtBytes(n: number): string {
  if (n < 1048576) return `${Math.max(1, Math.round(n / 1024))} KB`;
  if (n < 1073741824) return `${Math.round(n / 1048576)} MB`;
  return `${(n / 1073741824).toFixed(1)} GB`;
}

export function lineDiff(before: string, after: string): Array<[string, string]> {
  const a = before.split("\n");
  const b = after.split("\n");
  const dp = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0));
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const out: Array<[string, string]> = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push([" ", a[i]]);
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      out.push(["-", a[i]]);
      i++;
    } else {
      out.push(["+", b[j]]);
      j++;
    }
  }
  while (i < a.length) {
    out.push(["-", a[i]]);
    i++;
  }
  while (j < b.length) {
    out.push(["+", b[j]]);
    j++;
  }
  return out;
}

export function insertIntoSection(text: string, section: string, lines: string[]): string {
  const marker = `[${section}]`;
  const idx = text.indexOf(marker);
  if (idx < 0) return `${text.trimEnd()}\n\n${marker}\n${lines.join("\n")}\n`;
  const nl = text.indexOf("\n", idx + marker.length);
  const pos = nl < 0 ? text.length : nl + 1;
  return text.slice(0, pos) + lines.join("\n") + "\n" + text.slice(pos);
}
