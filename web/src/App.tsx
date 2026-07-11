import {
  autocompletion,
  CompletionContext,
  completionKeymap,
} from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { bracketMatching, defaultHighlightStyle, foldGutter, indentOnInput, StreamLanguage, syntaxHighlighting } from "@codemirror/language";
import { searchKeymap } from "@codemirror/search";
import { EditorState, Extension } from "@codemirror/state";
import { drawSelection, EditorView, highlightActiveLine, highlightActiveLineGutter, highlightSpecialChars, keymap, lineNumbers } from "@codemirror/view";
import {
  Activity,
  AlertTriangle,
  Boxes,
  Check,
  ChevronLeft,
  CircleStop,
  Container,
  Cpu,
  Download,
  Eye,
  FileClock,
  Gauge,
  Gpu,
  HardDrive,
  Home,
  Import,
  KeyRound,
  Package,
  ListFilter,
  LogOut,
  Logs,
  Menu,
  Monitor,
  Moon,
  Network,
  PanelLeftClose,
  PanelLeftOpen,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Save,
  Search,
  Server,
  Settings,
  Share2,
  Shield,
  SquarePen,
  Sun,
  Trash2,
  Upload,
  UserRound,
  Users,
  X,
  Zap,
} from "lucide-react";
import React, { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, Navigate, Route, Routes, useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { defaultAuth, IMPORT_MODES, TEMPLATES } from "./constants";
import {
  AuthState,
  AuditEvent,
  fmtBytes,
  GPUDevice,
  HostInfo,
  insertIntoSection,
  LicenseStatus,
  ManagedNode,
  NodeCounts,
  NodeGroup,
  PolicyFinding,
  Resource,
  lineDiff,
  stateClass,
  stateLabel,
  Unit,
  UnitState,
  UpdateInfo,
  ValidationResult,
} from "./lib";

type Toast = { id: number; message: string; error?: boolean };
type ThemeMode = "dark" | "light" | "auto";

type ApiContext = {
  auth: AuthState;
  setAuth: React.Dispatch<React.SetStateAction<AuthState>>;
  toast: (message: string, error?: boolean) => void;
};

const ApiContext = React.createContext<ApiContext | null>(null);

function useApiContext() {
  const ctx = React.useContext(ApiContext);
  if (!ctx) throw new Error("missing api context");
  return ctx;
}

async function request<T>(path: string, opts: RequestInit = {}, setAuth?: React.Dispatch<React.SetStateAction<AuthState>>): Promise<{ status: number; body: T }> {
  const headers = opts.body instanceof FormData ? opts.headers : { "Content-Type": "application/json", ...(opts.headers || {}) };
  const res = await fetch(path, { ...opts, headers });
  if (res.status === 401 && path !== "/api/login") {
    setAuth?.((a) => ({ ...a, authenticated: false }));
    throw new Error("authentication required");
  }
  const body = (await res.json().catch(() => ({}))) as T & { error?: string };
  if (!res.ok && res.status !== 422) throw new Error(body.error || `${res.status} ${res.statusText}`);
  return { status: res.status, body };
}

function useApi() {
  const { setAuth } = useApiContext();
  return useCallback(<T,>(path: string, opts?: RequestInit) => request<T>(path, opts, setAuth), [setAuth]);
}

function AppErrorBoundary({ children }: { children: React.ReactNode }) {
  const [message, setMessage] = useState("");
  useEffect(() => {
    const onError = (ev: ErrorEvent) => setMessage(ev.message);
    const onRejection = (ev: PromiseRejectionEvent) => setMessage(String(ev.reason?.message || ev.reason));
    window.addEventListener("error", onError);
    window.addEventListener("unhandledrejection", onRejection);
    return () => {
      window.removeEventListener("error", onError);
      window.removeEventListener("unhandledrejection", onRejection);
    };
  }, []);
  return (
    <>
      {message && <div className="toast toast-error">UI error: {message}</div>}
      {children}
    </>
  );
}

export function App() {
  const [auth, setAuth] = useState<AuthState>(defaultAuth);
  const [loaded, setLoaded] = useState(false);
  const [host, setHost] = useState<HostInfo | null>(null);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [theme, setTheme] = useState<ThemeMode>(() => {
    const saved = localStorage.getItem("rookery-theme");
    return saved === "dark" || saved === "light" || saved === "auto" ? saved : "auto";
  });

  const toast = useCallback((message: string, error = false) => {
    const id = Date.now() + Math.random();
    setToasts((rows) => [...rows, { id, message, error }]);
    window.setTimeout(() => setToasts((rows) => rows.filter((t) => t.id !== id)), 5000);
  }, []);

  const loadAuth = useCallback(async () => {
    try {
      const { body } = await request<Partial<AuthState> & { oidc?: AuthState["oidc"] }>("/api/auth", {}, setAuth);
      setAuth({
        required: !!body.required,
        authenticated: !!body.authenticated,
        readOnly: !!body.readOnly,
        setupNeeded: !!body.setupNeeded,
        onboarding: !!body.onboarding,
        username: body.username || "",
        email: body.email || "",
        role: body.role || "",
        oidc: body.oidc || null,
        passwordLogin: body.passwordLogin !== false,
      });
    } catch {
      setAuth(defaultAuth);
    } finally {
      setLoaded(true);
    }
  }, []);

  const loadHost = useCallback(async () => {
    try {
      const { body } = await request<HostInfo>("/api/host", {}, setAuth);
      setHost(body);
    } catch {
      setHost(null);
    }
  }, []);

  useEffect(() => {
    loadAuth();
  }, [loadAuth]);

  useEffect(() => {
    if (!loaded || (auth.required && !auth.authenticated)) return;
    loadHost();
    const id = window.setInterval(loadHost, 10000);
    return () => window.clearInterval(id);
  }, [auth.authenticated, auth.required, loadHost, loaded]);

  useEffect(() => {
    localStorage.setItem("rookery-theme", theme);
    document.documentElement.dataset.theme = theme;
  }, [theme]);
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key !== "/" || ev.ctrlKey || ev.metaKey || ev.altKey) return;
      const el = ev.target as HTMLElement | null;
      if (el && ["INPUT", "TEXTAREA", "SELECT"].includes(el.tagName)) return;
      const input = document.querySelector<HTMLInputElement>(".searchbox input");
      if (input) {
        ev.preventDefault();
        input.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  if (!loaded) return <Splash />;
  if (auth.setupNeeded && !sessionStorage.getItem("rookery-setup-skip")) {
    return (
      <ApiContext.Provider value={{ auth, setAuth, toast }}>
        <SetupView reloadAuth={loadAuth} />
        <ToastStack toasts={toasts} />
      </ApiContext.Provider>
    );
  }
  if (auth.required && !auth.authenticated) {
    return (
      <ApiContext.Provider value={{ auth, setAuth, toast }}>
        <LoginView reloadAuth={loadAuth} />
        <ToastStack toasts={toasts} />
      </ApiContext.Provider>
    );
  }
  if (auth.onboarding) {
    return (
      <ApiContext.Provider value={{ auth, setAuth, toast }}>
        <OnboardingView reloadAuth={loadAuth} />
        <ToastStack toasts={toasts} />
      </ApiContext.Provider>
    );
  }

  return (
    <ApiContext.Provider value={{ auth, setAuth, toast }}>
      <AppErrorBoundary>
        <Shell host={host} reloadAuth={loadAuth} theme={theme} setTheme={setTheme}>
          <Routes>
            <Route path="/" element={<Dashboard host={host} />} />
            {RESOURCE_VIEWS.filter((v) => v.path !== "/images").map((v) => <Route key={v.path} path={v.path} element={v.runnable ? <UnitsPage view={v} /> : <ResourceList view={v} />} />)}
            <Route path="/images" element={<ImagesView view={RESOURCE_VIEWS.find((v) => v.path === "/images")!} />} />
            <Route path="/updates" element={<Navigate to="/images" replace />} />
            <Route path="/gpus" element={<Navigate to="/resources" replace />} />
            <Route path="/units" element={<Navigate to="/containers" replace />} />
            <Route path="/failed" element={<Navigate to="/containers?status=failed" replace />} />
            <Route path="/resources" element={<ResourcesView />} />
            <Route path="/fleet" element={<FleetView />} />
            <Route path="/policies" element={<PoliciesView />} />
            <Route path="/unit/:scope/:name" element={<UnitDetail />} />
            <Route path="/new" element={<AdminOnly><NewUnit /></AdminOnly>} />
            <Route path="/import" element={<AdminOnly><ImportView /></AdminOnly>} />
            <Route path="/secrets" element={<AdminOnly><SecretsView /></AdminOnly>} />
            <Route path="/settings" element={<SettingsView host={host} />} />
            <Route path="/users" element={<Navigate to="/settings" replace />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </Shell>
        <ToastStack toasts={toasts} />
      </AppErrorBoundary>
    </ApiContext.Provider>
  );
}

function Splash() {
  return <main className="splash"><BrandMark /><p className="muted">Loading Rookery...</p></main>;
}

function ToastStack({ toasts }: { toasts: Toast[] }) {
  return <div className="toast-stack">{toasts.map((t) => <div key={t.id} className={`toast ${t.error ? "toast-error" : ""}`}>{t.message}</div>)}</div>;
}

function AdminOnly({ children }: { children: React.ReactNode }) {
  const { auth } = useApiContext();
  if (auth.readOnly) return <Navigate to="/" replace />;
  return children;
}

function Shell({ host, reloadAuth, theme, setTheme, children }: { host: HostInfo | null; reloadAuth: () => Promise<void>; theme: ThemeMode; setTheme: (theme: ThemeMode) => void; children: React.ReactNode }) {
  const { auth, toast } = useApiContext();
  const api = useApi();
  const location = useLocation();
  const [moreOpen, setMoreOpen] = useState(false);
  const [newUnitOpen, setNewUnitOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => localStorage.getItem("rookery-sidebar") === "collapsed");
  const nav = navItems(auth.readOnly);
  // The Manage page currently open, if any — drives the context-aware "+ Add"
  // button in the header (Add container on /containers, Add network on /networks…).
  const currentView = RESOURCE_VIEWS.find((v) => isActive(location.pathname, v.path));
  // Per-type counts for the sidebar so a newcomer sees the system's shape at a
  // glance (and a "0" invites the first create). ponytail: reuses the units
  // poll; lift to a shared context only if the double-fetch shows up as load.
  const { units: navUnits } = useUnits(true);
  const { resources: navResources } = useResources(true);
  const [nodes, setNodes] = useState<ManagedNode[]>([]);
  const [license, setLicense] = useState<LicenseStatus | null>(null);
  const [nodeColors, setNodeColors] = useState<Record<string, string>>({});
  const [nodeSel, setNodeSel] = useState(() => localStorage.getItem("rookery-node") || "");
  useEffect(() => { localStorage.setItem("rookery-node", nodeSel); }, [nodeSel]);
  useEffect(() => { api<{ nodes?: ManagedNode[]; license?: LicenseStatus }>("/api/nodes").then(({ body }) => { const ns = body.nodes || []; setNodes(ns); setLicense(body.license || null); setNodeColors(Object.fromEntries(ns.filter((n) => n.color).map((n) => [n.id, n.color!]))); setNodeSel((prev) => prev && !ns.some((n) => n.id === prev) ? "" : prev); }).catch(() => undefined); }, [api]);
  const atNodeLimit = !!license && !license.enterpriseAvailable && license.nodesRemaining <= 0;
  const navCounts = useMemo(() => {
    const c: Record<string, number> = {};
    RESOURCE_VIEWS.forEach((v) => { c[v.path] = 0; });
    // runnable types (containers/pods) come from Quadlet units; networks,
    // volumes and images come from live podman resources (which is why they
    // were showing 0 when counted as units). Counts follow the node picker.
    navUnits.filter((u) => onNode(nodeSel, u.node)).forEach((u) => { const v = viewForKind(u.kind); if (v === "/containers" || v === "/pods") c[v] = (c[v] || 0) + 1; });
    navResources.filter((r) => onNode(nodeSel, r.node)).forEach((r) => { const v = viewForKind(r.kind); c[v] = (c[v] || 0) + 1; });
    c["/fleet"] = nodes.length;
    return c;
  }, [navUnits, navResources, nodes.length, nodeSel]);

  useEffect(() => {
    localStorage.setItem("rookery-sidebar", sidebarCollapsed ? "collapsed" : "expanded");
  }, [sidebarCollapsed]);

  async function logout() {
    try {
      await api("/api/logout", { method: "POST" });
    } catch {
      // Session may already be gone.
    }
    await reloadAuth();
  }

  async function copyShare() {
    try {
      const { body } = await api<{ token: string }>("/api/share", { method: "POST", body: "{}" });
      const url = `${locationOrigin()}/?share=${encodeURIComponent(body.token)}`;
      await navigator.clipboard.writeText(url);
      toast("read-only link copied; valid 7 days");
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  return (
    <NodeColorContext.Provider value={nodeColors}>
    <NodeSelContext.Provider value={{ sel: nodeSel, setSel: setNodeSel, nodes }}>
    <div className={`app-shell ${sidebarCollapsed ? "sidebar-collapsed" : ""}`}>
      <aside className="sidebar">
        <div className="sidebar-brand-row">
          <Link to="/" className="brand" title="Rookery"><BrandMark /><span className="brand-text">Rookery</span></Link>
          <button className="btn icon-only collapse-btn" onClick={() => setSidebarCollapsed((v) => !v)} title={sidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"} aria-label={sidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"}>
            {sidebarCollapsed ? <PanelLeftOpen size={17} /> : <PanelLeftClose size={17} />}
          </button>
        </div>
        {nodes.length > 1 && (
          <select className="input node-picker" value={nodeSel} onChange={(e) => setNodeSel(e.target.value)} title="Node to manage" aria-label="Node to manage">
            <option value="">All nodes</option>
            {nodes.map((n) => <option key={n.id} value={n.id}>{n.displayName || (n.local ? "local" : n.id)}</option>)}
          </select>
        )}
        <nav className="side-nav">{groupedNavItems(nav).map((group) => (
          <div className="nav-group" key={group.name}>
            <div className="nav-group-label">{group.name}</div>
            {group.items.map((item) => <NavLinkItem key={item.to} item={item} active={isActive(location.pathname, item.to)} count={navCounts[item.to]} />)}
          </div>
        ))}</nav>
        <div className="sidebar-foot">
          {auth.username && <span className="user-chip" title={auth.username}><UserRound size={14} /><span>{auth.username}{auth.readOnly ? " (view)" : ""}</span></span>}
          {auth.required && auth.authenticated && <button className="icon-line" onClick={logout} title="Log out"><LogOut size={15} /><span>Log out</span></button>}
        </div>
      </aside>
      <div className="workbench">
        <header className="topbar">
          <div className="host-chips">
            {host?.metrics?.hostname && <span className="chip" title={host.metrics.kernel}> {host.metrics.hostname}</span>}
            {host?.podman?.version && <span className="chip">podman {host.podman.version}</span>}
            {host?.selinuxEnforcing && <span className="chip">SELinux</span>}
            {host && !host.generatorAvailable && <span className="chip chip-warn">no validator</span>}
            {auth.readOnly && <span className="chip chip-warn"><Eye size={13} /> read-only</span>}
          </div>
          <div className="top-actions">
            <ThemeSwitch theme={theme} setTheme={setTheme} />
            {!auth.readOnly && currentView && <button className="btn btn-accent" onClick={() => setNewUnitOpen(true)}><Plus size={16} /> Add {currentView.singular}</button>}
            {!auth.readOnly && auth.role === "admin" && location.pathname === "/fleet" && (atNodeLimit
              ? <button className="btn btn-accent" disabled title="Node limit reached"><Plus size={16} /> Add node</button>
              : <Link className="btn btn-accent" to="/fleet?new=1"><Plus size={16} /> Add node</Link>)}
            {!auth.readOnly && auth.required && auth.authenticated && <button className="btn btn-ghost" onClick={copyShare}><Share2 size={16} /> Share</button>}
            <button className="btn icon-only mobile-more" onClick={() => setMoreOpen((v) => !v)} aria-label="More"><Menu size={18} /></button>
          </div>
        </header>
        {moreOpen && (
          <div className="mobile-menu">
            {nav.map((item) => <NavLinkItem key={item.to} item={item} active={isActive(location.pathname, item.to)} onClick={() => setMoreOpen(false)} />)}
            {auth.required && auth.authenticated && <button className="icon-line" onClick={logout}><LogOut size={16} /> Log out</button>}
          </div>
        )}
        <main className="content">{children}</main>
      </div>
      <nav className="bottom-nav">
        {mobileNavItems(nav).map((item) => <NavLinkItem key={item.to} item={item} active={isActive(location.pathname, item.to)} compact />)}
      </nav>
      {newUnitOpen && (
        <Overlay title={`New ${currentView?.singular || "unit"}`} onClose={() => setNewUnitOpen(false)}>
          <CreateFlow kind={currentView?.singular || "container"} onCreated={() => setNewUnitOpen(false)} />
        </Overlay>
      )}
    </div>
    </NodeSelContext.Provider>
    </NodeColorContext.Provider>
  );
}

function ThemeSwitch({ theme, setTheme }: { theme: ThemeMode; setTheme: (theme: ThemeMode) => void }) {
  const options: Array<[ThemeMode, React.ReactNode, string]> = [
    ["auto", <Monitor size={15} />, "Use system theme"],
    ["light", <Sun size={15} />, "Light theme"],
    ["dark", <Moon size={15} />, "Dark theme"],
  ];
  return (
    <div className="segmented theme-switch" aria-label="Theme">
      {options.map(([mode, icon, label]) => (
        <button key={mode} className={theme === mode ? "active" : ""} title={label} aria-label={label} onClick={() => setTheme(mode)}>{icon}</button>
      ))}
    </div>
  );
}

function locationOrigin() {
  return window.location.origin;
}

function highlightMatches(line: string, needle: string): React.ReactNode[] {
  const lower = line.toLowerCase();
  const out: React.ReactNode[] = [];
  let i = 0;
  for (;;) {
    const at = lower.indexOf(needle, i);
    if (at < 0) {
      out.push(line.slice(i));
      return out;
    }
    if (at > i) out.push(line.slice(i, at));
    out.push(<mark key={at}>{line.slice(at, at + needle.length)}</mark>);
    i = at + needle.length;
  }
}

function failureSummary(failed: Array<{ name: string; error?: string }>) {
  const parts = failed.slice(0, 3).map((r) => `${r.name}: ${r.error || "failed"}`);
  if (failed.length > 3) parts.push(`+${failed.length - 3} more`);
  return parts.join("; ");
}

// The typed resource views under "Manage" — one page per Quadlet kind family.
// `kinds` is the set of unit kinds this page owns; `blurb` teaches a newcomer
// what the type is (shown on the empty state). Every kind maps to exactly one
// view via viewForKind, so no unit falls through.
// `runnable` marks types with a running/stopped lifecycle (containers, pods) —
// only those get the running/failed/stopped status filter. Networks and volumes
// are declarative resources that just exist, so the pills don't apply.
type ResourceView = { path: string; label: string; singular: string; icon: React.ElementType; kinds: string[]; catchAll?: boolean; runnable?: boolean; blurb: string };
const RESOURCE_VIEWS: ResourceView[] = [
  { path: "/containers", label: "Containers", singular: "container", icon: Container, kinds: ["container"], catchAll: true, runnable: true, blurb: "One service running from an image." },
  { path: "/pods", label: "Pods", singular: "pod", icon: Boxes, kinds: ["pod", "kube"], runnable: true, blurb: "Containers grouped and managed together." },
  { path: "/networks", label: "Networks", singular: "network", icon: Network, kinds: ["network"], blurb: "Lets containers reach each other by name." },
  { path: "/volumes", label: "Volumes", singular: "volume", icon: HardDrive, kinds: ["volume"], blurb: "Storage that outlives the container." },
  { path: "/images", label: "Images", singular: "image", icon: Package, kinds: ["image", "build"], blurb: "The templates your containers run from." },
];

// Which Manage page owns a given unit kind. Anything unmapped (incl. plain
// containers and unknown kinds) lands on Containers, the catch-all.
const KIND_TO_VIEW: Record<string, string> = Object.fromEntries(
  RESOURCE_VIEWS.filter((v) => !v.catchAll).flatMap((v) => v.kinds.map((k) => [k, v.path])),
);
function viewForKind(kind: string) {
  return KIND_TO_VIEW[kind] || "/containers";
}

function navItems(readOnly: boolean) {
  const base: Array<{ to: string; label: string; icon: React.ElementType; group: string; admin?: boolean }> = [
    { to: "/", label: "Dashboard", icon: Home, group: "Observe" },
    ...RESOURCE_VIEWS.map((v) => ({ to: v.path, label: v.label, icon: v.icon, group: "Manage" })),
    { to: "/fleet", label: "Fleet", icon: Server, group: "Govern" },
    { to: "/policies", label: "Policy", icon: Shield, group: "Govern" },
    { to: "/resources", label: "Resources", icon: Cpu, group: "Govern" },
    { to: "/import", label: "Import", icon: Import, group: "Admin", admin: true },
    { to: "/secrets", label: "Secrets", icon: KeyRound, group: "Admin", admin: true },
    { to: "/settings", label: "Settings", icon: Settings, group: "Admin" },
  ];
  return base.filter((item) => !readOnly || !item.admin);
}

// Couch-triage priority for the 5 bottom-nav slots; anything not listed
// fills remaining slots in sidebar order (matters for viewer accounts,
// whose nav lacks the admin items).
const MOBILE_NAV_ORDER = ["/", "/containers", "/pods", "/fleet", "/settings"];

function mobileNavItems<T extends { to: string }>(items: T[]) {
  const prioritized = MOBILE_NAV_ORDER.flatMap((to) => {
    const item = items.find((i) => i.to === to);
    return item ? [item] : [];
  });
  const rest = items.filter((i) => !MOBILE_NAV_ORDER.includes(i.to));
  return [...prioritized, ...rest].slice(0, 5);
}

function groupedNavItems(items: Array<{ to: string; label: string; icon: React.ElementType; group?: string }>) {
  const order = ["Observe", "Manage", "Govern", "Admin"];
  return order.map((name) => ({ name, items: items.filter((item) => (item.group || "Manage") === name) })).filter((group) => group.items.length);
}

function NavLinkItem({ item, active, compact, onClick, count }: { item: { to: string; label: string; icon: React.ElementType }; active: boolean; compact?: boolean; onClick?: () => void; count?: number }) {
  const Icon = item.icon;
  return (
    <Link onClick={onClick} className={`${compact ? "bottom-link" : "nav-link"} ${active ? "active" : ""}`} to={item.to} title={item.label}>
      <Icon size={compact ? 20 : 17} />
      <span>{item.label}</span>
      {!compact && count !== undefined && <span className="nav-count">{count}</span>}
    </Link>
  );
}

function isActive(pathname: string, to: string) {
  if (to === "/") return pathname === "/";
  return pathname === to || pathname.startsWith(`${to}/`);
}

// The Noto Emoji seal (Apache-2.0) on the brand badge — the same seal the
// 🦭 emoji renders, but consistent across platforms and crisp at any size.
function BrandMark() {
  return <img className="brand-mark" src="/logo.svg" alt="" aria-hidden="true" />;
}

function LoginView({ reloadAuth }: { reloadAuth: () => Promise<void> }) {
  const { auth } = useApiContext();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setError("");
    try {
      await request("/api/login", { method: "POST", body: JSON.stringify({ username, password }) });
      await reloadAuth();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <main className="auth-page">
      <section className="auth-card">
        <div className="brand large"><BrandMark /><span>Rookery</span></div>
        <p className="muted">Sign in to manage this host's Quadlets.</p>
        {auth.oidc?.enabled && <a className="btn btn-accent full" href="/api/oidc/login">Sign in with {auth.oidc.name || "SSO"}</a>}
        {auth.oidc?.enabled && auth.passwordLogin && <div className="separator"><span>or</span></div>}
        {auth.passwordLogin && (
          <form onSubmit={submit} className="stack-form">
            <input className="input" placeholder="Username or email" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
            <input className="input" placeholder="Password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            <button className="btn btn-accent full">Sign in</button>
          </form>
        )}
        {error && <p className="banner banner-error">{error}</p>}
      </section>
    </main>
  );
}

function SetupView({ reloadAuth }: { reloadAuth: () => Promise<void> }) {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [error, setError] = useState("");

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    if (password !== repeat) {
      setError("passwords do not match");
      return;
    }
    try {
      await request("/api/setup", { method: "POST", body: JSON.stringify({ username, password }) });
      await reloadAuth();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <main className="auth-page">
      <section className="auth-card">
        <div className="brand large"><BrandMark /><span>Welcome to Rookery</span></div>
        <p className="muted">Create the first admin account stored on this host.</p>
        <form onSubmit={submit} className="stack-form">
          <input className="input" placeholder="Username" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
          <input className="input" placeholder="Password (min 8 characters)" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          <input className="input" placeholder="Repeat password" type="password" autoComplete="new-password" value={repeat} onChange={(e) => setRepeat(e.target.value)} />
          <button className="btn btn-accent full">Create admin account</button>
        </form>
        {error && <p className="banner banner-error">{error}</p>}
        <button className="link-button" onClick={() => { sessionStorage.setItem("rookery-setup-skip", "1"); window.location.hash = "#/"; window.location.reload(); }}>Skip for now</button>
      </section>
    </main>
  );
}

function OnboardingView({ reloadAuth }: { reloadAuth: () => Promise<void> }) {
  const { auth, toast } = useApiContext();
  const [email, setEmail] = useState(auth.email === "admin@example.com" ? "" : auth.email);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [error, setError] = useState("");

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setError("");
    if (newPassword !== repeat) {
      setError("new passwords do not match");
      return;
    }
    try {
      await request("/api/onboarding", {
        method: "POST",
        body: JSON.stringify({ email, currentPassword, newPassword }),
      });
      toast("admin profile updated");
      await reloadAuth();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <main className="auth-page">
      <section className="auth-card">
        <div className="brand large"><BrandMark /><span>Finish setup</span></div>
        <p className="muted">Update the admin email and replace the temporary or configured first-login password.</p>
        <form onSubmit={submit} className="stack-form">
          <input className="input" placeholder="Admin email" type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          <input className="input" placeholder="Current password" type="password" autoComplete="current-password" value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)} />
          <input className="input" placeholder="New password (min 8 characters)" type="password" autoComplete="new-password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} />
          <input className="input" placeholder="Repeat new password" type="password" autoComplete="new-password" value={repeat} onChange={(e) => setRepeat(e.target.value)} />
          <button className="btn btn-accent full">Save and continue</button>
        </form>
        {error && <p className="banner banner-error">{error}</p>}
      </section>
    </main>
  );
}

function useUnits(poll = false) {
  const api = useApi();
  const [units, setUnits] = useState<Unit[]>([]);
  const [scopeErrors, setScopeErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const load = useCallback(async () => {
    try {
      const { body } = await api<{ units?: Unit[]; scopeErrors?: Record<string, string> }>("/api/units");
      setUnits(body.units || []);
      setScopeErrors(body.scopeErrors || {});
      setError("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [api]);
  useEffect(() => {
    load();
    if (!poll) return;
    const id = window.setInterval(() => {
      if (!document.hidden) load();
    }, 5000);
    return () => window.clearInterval(id);
  }, [load, poll]);
  return { units, scopeErrors, error, loading, reload: load };
}

function useResources(poll = false) {
  const api = useApi();
  const [resources, setResources] = useState<Resource[]>([]);
  const [scopeErrors, setScopeErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const load = useCallback(async () => {
    try {
      const { body } = await api<{ resources?: Resource[]; scopeErrors?: Record<string, string> }>("/api/resources");
      setResources(body.resources || []);
      setScopeErrors(body.scopeErrors || {});
      setError("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [api]);
  useEffect(() => {
    load();
    if (!poll) return;
    const id = window.setInterval(() => { if (!document.hidden) load(); }, 8000);
    return () => window.clearInterval(id);
  }, [load, poll]);
  return { resources, scopeErrors, error, loading, reload: load };
}

function useDirtyGuard(dirty: boolean, message = "Discard unsaved changes?") {
  useEffect(() => {
    if (!dirty) return;
    const beforeUnload = (ev: BeforeUnloadEvent) => {
      ev.preventDefault();
      ev.returnValue = message;
    };
    const click = (ev: MouseEvent) => {
      const target = ev.target as Element | null;
      const link = target?.closest?.("a[href]") as HTMLAnchorElement | null;
      if (!link || link.target || link.download || ev.defaultPrevented) return;
      if (link.origin === window.location.origin && !window.confirm(message)) {
        ev.preventDefault();
        ev.stopPropagation();
      }
    };
    window.addEventListener("beforeunload", beforeUnload);
    document.addEventListener("click", click, true);
    return () => {
      window.removeEventListener("beforeunload", beforeUnload);
      document.removeEventListener("click", click, true);
    };
  }, [dirty, message]);
}

const QUADLET_KEYS: Record<string, string[]> = {
  Container: ["AddCapability", "AddDevice", "ContainerName", "DropCapability", "Environment", "Exec", "HealthCmd", "HealthInterval", "HealthRetries", "HealthStartPeriod", "HealthTimeout", "Image", "Label", "Network", "NoHealthcheck", "Pod", "PublishPort", "Secret", "SecurityLabelDisable", "User", "Volume", "WorkingDir"],
  Pod: ["GlobalArgs", "Network", "PodName", "PublishPort", "UserNS"],
  Network: ["DisableDNS", "Driver", "Gateway", "IPv6", "Internal", "Label", "NetworkName", "Subnet"],
  Volume: ["Copy", "Device", "Driver", "Group", "Image", "Label", "Options", "Type", "User", "VolumeName"],
  Kube: ["ConfigMap", "ExitCodePropagation", "GlobalArgs", "KubeDownForce", "Network", "PublishPort", "Yaml"],
  Image: ["AllTags", "Arch", "AuthFile", "CertDir", "Creds", "Image", "ImageTag", "OS", "TLSVerify", "Variant"],
  Build: ["Annotation", "Arch", "AuthFile", "BuildArg", "CacheFrom", "CacheTo", "DNS", "File", "ImageTag", "Label", "Network", "Pull", "SetLabel", "Target", "TLSVerify"],
  Unit: ["After", "Before", "Description", "Documentation", "Requires", "Wants"],
  Service: ["Environment", "ExecStartPre", "ExecStopPost", "Restart", "RestartSec", "TimeoutStartSec", "TimeoutStopSec", "Type"],
  Install: ["Alias", "RequiredBy", "WantedBy"],
};

function sectionAt(doc: string, pos: number) {
  const before = doc.slice(0, pos).split("\n").reverse();
  for (const line of before) {
    const m = line.trim().match(/^\[([^\]]+)\]$/);
    if (m) return m[1];
  }
  return "";
}

function quadletCompletions(ctx: CompletionContext) {
  const word = ctx.matchBefore(/[A-Za-z]*$/);
  if (!word || (word.from === word.to && !ctx.explicit)) return null;
  const section = sectionAt(ctx.state.doc.toString(), ctx.pos);
  const keys = QUADLET_KEYS[section] || Object.values(QUADLET_KEYS).flat();
  return {
    from: word.from,
    options: keys.map((key) => ({ label: `${key}=`, type: "property" })),
  };
}

const unitFileLanguage = StreamLanguage.define({
  token(stream) {
    if (stream.sol() && stream.match(/^\s*[#;].*/)) return "comment";
    if (stream.sol() && stream.match(/^\[[^\]]*\]\s*$/)) return "keyword";
    if (stream.sol() && stream.match(/^[A-Za-z][\w.-]*(?=\s*=)/)) return "propertyName";
    if (stream.match(/^=/)) return "operator";
    stream.skipToEnd();
    return null;
  },
});

function CodeEditor({ value, onChange, readOnly, small, short, onSave }: { value: string; onChange: (value: string) => void; readOnly?: boolean; small?: boolean; short?: boolean; onSave?: () => void }) {
  const host = useRef<HTMLDivElement | null>(null);
  const view = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  const onSaveRef = useRef(onSave);
  useEffect(() => { onChangeRef.current = onChange; onSaveRef.current = onSave; }, [onChange, onSave]);
  useEffect(() => {
    if (!host.current) return;
    const extensions: Extension[] = [
      lineNumbers(),
      foldGutter(),
      highlightSpecialChars(),
      history(),
      drawSelection(),
      indentOnInput(),
      unitFileLanguage,
      syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
      bracketMatching(),
      autocompletion({ override: [quadletCompletions] }),
      highlightActiveLine(),
      highlightActiveLineGutter(),
      keymap.of([
        { key: "Mod-s", run: () => { onSaveRef.current?.(); return true; } },
        ...defaultKeymap,
        ...historyKeymap,
        ...completionKeymap,
        ...searchKeymap,
      ]),
      EditorView.lineWrapping,
      EditorView.updateListener.of((update) => {
        if (update.docChanged) onChangeRef.current(update.state.doc.toString());
      }),
      EditorView.editable.of(!readOnly),
      EditorState.readOnly.of(!!readOnly),
    ];
    view.current = new EditorView({ parent: host.current, state: EditorState.create({ doc: value, extensions }) });
    return () => {
      view.current?.destroy();
      view.current = null;
    };
  }, [readOnly]);
  useEffect(() => {
    const v = view.current;
    if (!v || v.state.doc.toString() === value) return;
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } });
  }, [value]);
  return <div ref={host} className={`code-editor cm-editor-wrap ${small ? "small" : ""} ${short ? "short" : ""}`} />;
}

function Dashboard({ host }: { host: HostInfo | null }) {
  const { units: allUnits, scopeErrors, error, loading, reload } = useUnits(true);
  const [allGpus, setAllGpus] = useState<GPUDevice[]>([]);
  const { sel, nodes } = useNodeSel();
  const selNode = nodes.find((n) => n.id === sel);
  const api = useApi();

  useEffect(() => {
    api<{ devices?: GPUDevice[] }>("/api/gpus").then(({ body }) => setAllGpus(body.devices || [])).catch(() => setAllGpus([]));
  }, [api]);

  const units = useMemo(() => allUnits.filter((u) => onNode(sel, u.node)), [allUnits, sel]);
  const gpus = allGpus.filter((d) => gpuOnNode(d, selNode));
  const model = useMemo(() => summarizeUnits(units), [units]);
  const failed = units.filter((u) => stateClass(u) === "failed" || u.health === "unhealthy");
  // With a node selected, the metric tiles show that node's health (from the
  // fleet inventory); "All nodes" keeps the local host's, as before.
  const m = (selNode && !selNode.local ? selNode.metrics : host?.metrics) || {};
  const memPct = m.memTotalKb ? Math.round(100 * (1 - (m.memAvailKb || 0) / m.memTotalKb)) : null;

  return (
    <Page title="Dashboard" kicker={sel ? `Node ${selNode?.displayName || sel}` : "Fleet overview"}>
      <ScopeErrors errors={scopeErrors} />
      {error && <p className="banner banner-error">{error}</p>}
      {units.length > 0 && (
        <div className="colony-strip" aria-label="Every unit by state">
          {units.map((u) => {
            const cls = u.health === "unhealthy" ? "failed" : stateClass(u);
            return (
              <Link
                key={`${u.scope}/${u.name}`}
                className={`colony-tick ${cls}`}
                title={`${u.name} · ${stateLabel(u)}`}
                aria-label={`${u.name}: ${stateLabel(u)}`}
                to={`/unit/${encodeURIComponent(u.scope)}/${encodeURIComponent(u.name)}`}
              />
            );
          })}
        </div>
      )}
      <div className="tiles">
        <MetricTile label="containers" value={`${model.runningContainers}/${model.containers}`} tone={model.runningContainers ? "ok" : "dim"} />
        <MetricTile label="pods" value={`${model.runningPods}/${model.pods}`} tone={model.runningPods ? "ok" : "dim"} />
        <MetricTile label="failed" value={model.failed} tone={model.failed ? "bad" : "dim"} />
        <MetricTile label="stopped" value={model.stopped + model.unknown} tone="dim" />
        {m.cpuPct != null && m.cpuPct >= 0 && <MetricTile label="cpu" value={`${m.cpuPct}%`} meter={m.cpuPct} />}
        {m.load1 != null && <MetricTile label={m.cores ? `load 1m · ${m.cores} cores` : "load 1m"} value={m.load1.toFixed(2)} />}
        {memPct != null && <MetricTile label="memory" value={`${memPct}%`} meter={memPct} />}
        {gpus.length > 0 && <MetricTile label="gpus" value={gpus.length} tone="ok" />}
      </div>
      {loading ? <p className="muted">Loading units...</p> : units.length === 0 ? <EmptyState title="No Quadlet units found" text="Create a unit or import an existing container configuration." /> : null}
      <div className="dashboard-grid">
        <Panel title="Needs attention" icon={AlertTriangle}>
          {failed.length ? failed.slice(0, 6).map((u) => <UnitRow key={`${u.scope}/${u.name}`} unit={u} onChanged={reload} compact />) : <p className="muted">No failed units.</p>}
        </Panel>
        <Panel title="GPU inventory" icon={Cpu}>
          {gpus.length ? gpus.slice(0, 4).map((d) => <GpuRow key={`${d.host || "local"}-${d.name}`} device={d} />) : <p className="muted">No GPUs detected.</p>}
        </Panel>
      </div>
      <Panel title="Recent operational view" icon={Gauge}>
        <div className="unit-list compact">{units.slice(0, 10).map((u) => <UnitRow key={`${u.scope}/${u.name}`} unit={u} onChanged={reload} compact />)}</div>
      </Panel>
    </Page>
  );
}

function summarizeUnits(units: Unit[]) {
  const infra = new Set(["network", "volume", "image", "build"]);
  const svc = units.filter((u) => !infra.has(u.kind));
  const containers = svc.filter((u) => u.kind !== "pod");
  const pods = svc.filter((u) => u.kind === "pod");
  const count = (list: Unit[], state: UnitState) => list.filter((u) => stateClass(u) === state).length;
  return {
    containers: containers.length,
    pods: pods.length,
    runningContainers: count(containers, "running"),
    runningPods: count(pods, "running"),
    failed: count(svc, "failed"),
    stopped: count(svc, "stopped"),
    unknown: count(svc, "unknown"),
  };
}

function UnitsPage({ view }: { view: ResourceView }) {
  const { units, scopeErrors, error, loading, reload } = useUnits(true);
  const { auth, toast } = useApiContext();
  const api = useApi();
  const [params, setParams] = useSearchParams();
  const [q, setQ] = useState(params.get("q") || "");
  const [scope, setScope] = useState(params.get("scope") || "all");
  const [status, setStatus] = useState<UnitState | "all">((params.get("status") as UnitState | "all") || "all");
  const [sort, setSort] = useState(params.get("sort") || "name");
  const [compact, setCompact] = useState(() => localStorage.getItem("rookery-units-density") === "compact");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState("");
  const { sel } = useNodeSel();
  const ofType = useMemo(() => units.filter((u) => viewForKind(u.kind) === view.path && onNode(sel, u.node)), [units, view.path, sel]);
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const rows = ofType.filter((u) => {
      const cls = stateClass(u);
      if (scope !== "all" && u.scope !== scope) return false;
      if (status !== "all" && cls !== status) return false;
      return !needle || `${u.name} ${u.description || ""} ${u.image || ""} ${u.pod || ""}`.toLowerCase().includes(needle);
    });
    return rows.sort((a, b) => {
      const av = sort === "state" ? stateLabel(a) : sort === "scope" ? a.scope : sort === "kind" ? a.kind : a.name;
      const bv = sort === "state" ? stateLabel(b) : sort === "scope" ? b.scope : sort === "kind" ? b.kind : b.name;
      return av.localeCompare(bv);
    });
  }, [ofType, q, scope, sort, status]);
  const scopes = ["all", ...Array.from(new Set(ofType.map((u) => u.scope))).sort()];
  const selectedUnits = filtered.filter((u) => selected.has(`${u.scope}/${u.name}`));
  const statusCounts = useMemo(() => {
    const counts: Record<UnitState | "all", number> = { all: ofType.length, running: 0, failed: 0, pending: 0, stopped: 0, unknown: 0 };
    ofType.forEach((u) => { counts[stateClass(u)] += 1; });
    return counts;
  }, [ofType]);

  useEffect(() => {
    localStorage.setItem("rookery-units-density", compact ? "compact" : "comfortable");
  }, [compact]);
  useEffect(() => {
    const next = new URLSearchParams();
    if (q) next.set("q", q);
    if (scope !== "all") next.set("scope", scope);
    if (status !== "all") next.set("status", status);
    if (sort !== "name") next.set("sort", sort);
    setParams(next, { replace: true });
  }, [q, scope, setParams, sort, status]);

  function toggleUnit(unit: Unit, checked: boolean) {
    const key = `${unit.scope}/${unit.name}`;
    setSelected((prev) => {
      const next = new Set(prev);
      if (checked) next.add(key);
      else next.delete(key);
      return next;
    });
  }

  function selectAllVisible(checked: boolean) {
    setSelected((prev) => {
      const next = new Set(prev);
      filtered.forEach((u) => {
        const key = `${u.scope}/${u.name}`;
        if (checked) next.add(key);
        else next.delete(key);
      });
      return next;
    });
  }

  async function bulkAction(action: string) {
    if (!selectedUnits.length) return;
    if ((action === "stop" || action === "restart") && !confirm(`${action} ${selectedUnits.length} selected units?`)) return;
    setBulkBusy(action);
    try {
      const { body } = await api<{ results?: Array<{ scope: string; name: string; ok: boolean; error?: string }> }>("/api/units/bulk-action", {
        method: "POST",
        body: JSON.stringify({ action, units: selectedUnits.map((u) => ({ scope: u.scope, name: u.name })) }),
      });
      const results = body.results || [];
      const failed = results.filter((r) => !r.ok);
      toast(failed.length ? `${action}: ${results.length - failed.length} ok — ${failureSummary(failed)}` : `${action}: ${results.length} ok`, failed.length > 0);
      setSelected(new Set());
      reload();
    } catch (e) {
      toast((e as Error).message, true);
    } finally {
      setBulkBusy("");
    }
  }

  const addBtn = !auth.readOnly && <Link className="btn btn-accent" to={`/new?kind=${view.singular}`}><Plus size={16} /> Add {view.singular}</Link>;
  return (
    <Page title={view.label} subtitle={view.blurb}>
      <ScopeErrors errors={scopeErrors} />
      {error && <p className="banner banner-error">{error}</p>}
      {view.runnable && (
        <div className="tiles">
          <MetricTile label={view.label.toLowerCase()} value={statusCounts.all} tone="dim" />
          <MetricTile label="running" value={statusCounts.running} tone={statusCounts.running ? "ok" : "dim"} />
          <MetricTile label="failed" value={statusCounts.failed} tone={statusCounts.failed ? "bad" : "dim"} />
          <MetricTile label="stopped" value={statusCounts.stopped} tone={statusCounts.stopped ? "warn" : "dim"} />
        </div>
      )}
      <div className="filterbar units-filterbar">
        <label className="searchbox"><Search size={16} /><input value={q} onChange={(e) => setQ(e.target.value)} placeholder={`Filter ${view.label.toLowerCase()} by name, image, pod...`} /></label>
        {view.runnable && <select className="input" value={status} onChange={(e) => setStatus(e.target.value as UnitState | "all")}>{(["all", "running", "failed", "pending", "stopped", "unknown"] as Array<UnitState | "all">).map((s) => <option key={s} value={s}>{s === "all" ? "all statuses" : s}</option>)}</select>}
        <select className="input" value={scope} onChange={(e) => setScope(e.target.value)}>{scopes.map((s) => <option key={s}>{s}</option>)}</select>
        <select className="input" value={sort} onChange={(e) => setSort(e.target.value)}><option value="name">sort name</option><option value="state">sort state</option><option value="scope">sort scope</option></select>
        <label className="check density-toggle"><input type="checkbox" checked={compact} onChange={(e) => setCompact(e.target.checked)} /> compact rows</label>
      </div>
      {!auth.readOnly && filtered.length > 0 && (
        <div className="action-row bulk-row">
          <label className="check"><input type="checkbox" checked={selectedUnits.length === filtered.length && filtered.length > 0} onChange={(e) => selectAllVisible(e.target.checked)} /> select visible</label>
          {selectedUnits.length > 0 && <span className="badge">{selectedUnits.length} selected</span>}
          {["start", "stop", "restart"].map((a) => <button key={a} className="btn btn-sm" disabled={!selectedUnits.length || !!bulkBusy} onClick={() => bulkAction(a)}>{bulkBusy === a ? <RefreshCw className="spin" size={14} /> : actionIcon(a)} {a}</button>)}
        </div>
      )}
      {loading ? <p className="muted">Loading units...</p> : filtered.length ? (
        <div className="unit-list">{filtered.map((u) => auth.readOnly
          ? <UnitRow key={`${u.scope}/${u.name}`} unit={u} onChanged={reload} compact={compact} />
          : <div className="select-row" key={`${u.scope}/${u.name}`}><input type="checkbox" checked={selected.has(`${u.scope}/${u.name}`)} onChange={(e) => toggleUnit(u, e.target.checked)} /><UnitRow unit={u} onChanged={reload} compact={compact} /></div>)}</div>
      ) : ofType.length === 0 ? (
        <EmptyState icon={view.icon} title={`No ${view.label.toLowerCase()} yet`} text={view.blurb} action={addBtn} />
      ) : (
        <EmptyState title={`No matching ${view.label.toLowerCase()}`} text="Adjust the filters above." />
      )}
    </Page>
  );
}

// ResourceList shows live podman networks/volumes (from /api/resources), each
// tagged managed (Quadlet-backed) or unmanaged (created imperatively) — so the
// pages reflect what podman actually has, not just the rare .network/.volume
// units. Networks/volumes have no run state, so there's no status filter.
function ResourceList({ view }: { view: ResourceView }) {
  const { resources, scopeErrors, error, loading, reload } = useResources(true);
  const { auth } = useApiContext();
  const { sel } = useNodeSel();
  const [q, setQ] = useState("");
  const ofType = resources.filter((res) => res.kind === view.singular && onNode(sel, res.node));
  const needle = q.trim().toLowerCase();
  const filtered = ofType.filter((res) => !needle || `${res.name} ${res.scope} ${res.driver || ""} ${res.detail || ""}`.toLowerCase().includes(needle)).sort((a, b) => a.name.localeCompare(b.name));
  const managed = ofType.filter((r) => r.managed).length;
  const addBtn = !auth.readOnly && <Link className="btn btn-accent" to={`/new?kind=${view.singular}`}><Plus size={16} /> Add {view.singular}</Link>;
  return (
    <Page title={view.label} subtitle={view.blurb}>
      <ScopeErrors errors={scopeErrors} />
      {error && <p className="banner banner-error">{error}</p>}
      <div className="tiles">
        <MetricTile label={view.label.toLowerCase()} value={ofType.length} tone="dim" />
        <MetricTile label="managed" value={managed} tone={managed ? "ok" : "dim"} />
        <MetricTile label="unmanaged" value={ofType.length - managed} tone={ofType.length - managed ? "warn" : "dim"} />
      </div>
      <div className="filterbar units-filterbar">
        <label className="searchbox"><Search size={16} /><input value={q} onChange={(e) => setQ(e.target.value)} placeholder={`Filter ${view.label.toLowerCase()} by name, driver...`} /></label>
      </div>
      {loading ? <p className="muted">Loading {view.label.toLowerCase()}...</p> : filtered.length ? (
        <div className="unit-list">{filtered.map((res) => <ResourceRow key={`${res.scope}/${res.kind}/${res.name}`} res={res} onChanged={reload} />)}</div>
      ) : ofType.length === 0 ? (
        <EmptyState icon={view.icon} title={`No ${view.label.toLowerCase()} yet`} text={view.blurb} action={addBtn} />
      ) : (
        <EmptyState title={`No matching ${view.label.toLowerCase()}`} text="Adjust the filter above." />
      )}
    </Page>
  );
}

function ResourceRow({ res, onChanged, updateAvailable }: { res: Resource; onChanged?: () => void; updateAvailable?: boolean }) {
  const { auth, toast } = useApiContext();
  const api = useApi();
  const nodeCol = useNodeColor();
  const [busy, setBusy] = useState(false);
  const [detailOpen, setDetailOpen] = useState(false);
  async function del(e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    if (!confirm(`Delete ${res.kind} "${res.name}" on ${res.scope}? This removes it from podman.`)) return;
    setBusy(true);
    try {
      await api(`/api/resources?scope=${encodeURIComponent(res.scope)}&kind=${encodeURIComponent(res.kind)}&name=${encodeURIComponent(res.name)}`, { method: "DELETE" });
      toast(`deleted ${res.name}`);
      onChanged?.();
    } catch (err) {
      toast((err as Error).message, true);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="unit-row resource-row">
      <div className="resource-click" onClick={() => setDetailOpen(true)}>
        <span className={`state-icon ${res.used ? "running" : "stopped"}`} title={res.used ? `${res.kind} — in use` : `${res.kind} — unused`}><KindIcon kind={res.kind} size={17} /></span>
        <span className="unit-main">
          <span className="unit-title">{res.name}</span>
          <span className="unit-sub">{[res.driver, res.detail].filter(Boolean).join(" · ") || res.scope}</span>
        </span>
        <span className="badges">
          {updateAvailable && <RowChip icon={Download} color="var(--warn)" label="update available">update available — pull to refresh</RowChip>}
          {/* only the exception (Quadlet-backed) is flagged; imperatively-created is the norm */}
          {res.managed && <span className="badge badge-running" title="defined by a Quadlet unit">managed</span>}
          {res.node && <RowChip icon={Server} color={nodeCol(res.node)} label={`node ${res.node}`}>node <b>{res.node}</b> · {res.scope}</RowChip>}
        </span>
      </div>
      {!auth.readOnly && !res.managed && (
        <span className="row-actions">
          <button className="btn icon-only" disabled={busy} title={`Delete ${res.kind}`} onClick={del}>{busy ? <RefreshCw className="spin" size={16} /> : <Trash2 size={16} />}</button>
        </span>
      )}
      {detailOpen && <ResourceDetail res={res} onClose={() => setDetailOpen(false)} />}
    </div>
  );
}

function ResourceDetail({ res, onClose }: { res: Resource; onClose: () => void }) {
  const api = useApi();
  const [detail, setDetail] = useState<{ fields: { key: string; value: string }[]; usedBy: string[] } | null>(null);
  const [note, setNote] = useState("");
  useEffect(() => {
    api<{ fields: { key: string; value: string }[]; usedBy: string[] }>(`/api/resources/inspect?scope=${encodeURIComponent(res.scope)}&kind=${encodeURIComponent(res.kind)}&name=${encodeURIComponent(res.name)}`)
      .then(({ body }) => setDetail(body)).catch((e) => setNote((e as Error).message));
  }, [api, res]);
  return (
    <Overlay title={res.name} onClose={onClose}>
      <dl className="kv">
        <dt>kind</dt><dd>{res.kind}</dd>
        <dt>scope</dt><dd>{res.scope}{res.node ? ` · ${res.node}` : ""}</dd>
        {!detail && res.driver && <><dt>driver</dt><dd>{res.driver}</dd></>}
        {!detail && res.detail && <><dt>detail</dt><dd>{res.detail}</dd></>}
        <dt>managed</dt><dd>{res.managed ? "yes — defined by a Quadlet unit" : "no — created imperatively"}</dd>
        {detail?.fields.map((f) => <React.Fragment key={f.key}><dt>{f.key}</dt><dd>{f.value}</dd></React.Fragment>)}
      </dl>
      {detail?.usedBy?.length ? (
        <div className="resource-usedby"><h3>Used by</h3><div>{detail.usedBy.map((u) => <span className="badge badge-user" key={u}>{u}</span>)}</div></div>
      ) : null}
      {note && <p className="muted">{note}</p>}
    </Overlay>
  );
}

// KindIcon maps a Quadlet kind to a glyph, so rows show a type icon instead of
// a text pill. Container/image/build/kube share the box; pods, networks and
// volumes get their own.
function KindIcon({ kind, size = 13 }: { kind: string; size?: number }) {
  switch (kind) {
    case "pod":
      return <Boxes size={size} />;
    case "network":
      return <Network size={size} />;
    case "volume":
      return <HardDrive size={size} />;
    case "image":
    case "build":
      return <Package size={size} />;
    default:
      return <Container size={size} />;
  }
}

// The page already names the type, so drop the Quadlet extension from row labels.
const QUADLET_EXT = /\.(container|pod|network|volume|image|build|kube)$/;
function displayName(name: string) {
  return name.replace(QUADLET_EXT, "");
}

// A stable color per node id (hashed to a hue) so the same node reads the same
// color everywhere — the Fleet swatch and each unit row's node chip.
function hslToHex(h: number, s: number, l: number) {
  s /= 100; l /= 100;
  const a = s * Math.min(l, 1 - l);
  const f = (n: number) => { const k = (n + h / 30) % 12; return l - a * Math.max(-1, Math.min(k - 3, Math.min(9 - k, 1))); };
  const hex = (x: number) => Math.round(255 * x).toString(16).padStart(2, "0");
  return `#${hex(f(0))}${hex(f(8))}${hex(f(4))}`;
}
// Hex (for <input type=color> compatibility) so a custom override and the auto
// color share the same format.
function nodeColor(id: string) {
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return hslToHex(h % 360, 62, 58);
}

// Custom per-node colors (from the Fleet edit dialog) override the auto hash.
// Shell provides the map so every unit/resource row's node chip matches the
// Fleet swatch. Falls back to the hash for nodes without an override.
const NodeColorContext = React.createContext<Record<string, string>>({});
function useNodeColor() {
  const overrides = React.useContext(NodeColorContext);
  return useCallback((id: string) => overrides[id] || nodeColor(id), [overrides]);
}

// Global node selection ("" = all nodes), set from the topbar picker or a
// Fleet row's manage button. Every list view and node-scoped action filters
// by it; Shell provides it alongside the color map.
const NodeSelContext = React.createContext<{ sel: string; setSel: (id: string) => void; nodes: ManagedNode[] }>({ sel: "", setSel: () => undefined, nodes: [] });
function useNodeSel() {
  return React.useContext(NodeSelContext);
}
// onNode: does a row tagged with rowNode belong to the selected node?
// Untagged rows are local.
function onNode(sel: string, rowNode?: string) {
  return !sel || (rowNode || "local") === sel;
}
// gpuOnNode: local GPUs carry no host tag; remote/agent GPUs are tagged with
// their node id (or hostname). No node = show everything.
function gpuOnNode(d: GPUDevice, node?: ManagedNode) {
  if (!node) return true;
  const hn = node.metrics?.hostname;
  return node.local ? (!d.host || d.host === "local" || d.host === hn) : (!!d.host && (d.host === node.id || d.host === hn));
}

// RowChip is a compact icon button with a hover/focus popover — used for
// per-row metadata (privilege, pod, usage, gpu) so a dense row stays scannable
// but details are one hover/tap away. Click is swallowed so it never triggers
// the row's navigation.
function RowChip({ icon: Icon, tone, color, label, children }: { icon: React.ElementType; tone?: string; color?: string; label: string; children?: React.ReactNode }) {
  return (
    <span className="row-chip-wrap">
      <button type="button" className={`row-chip ${tone || ""}`} style={color ? { color } : undefined} aria-label={label} onClick={(e) => { e.preventDefault(); e.stopPropagation(); }}>
        <Icon size={16} />
      </button>
      <span className="row-pop" role="tooltip">{children ?? label}</span>
    </span>
  );
}

function UnitRow({ unit, onChanged, compact = false }: { unit: Unit; onChanged: () => void; compact?: boolean }) {
  const { auth, toast } = useApiContext();
  const api = useApi();
  const navigate = useNavigate();
  const nodeCol = useNodeColor();
  const [acting, setActing] = useState("");
  const cls = stateClass(unit);
  const canStart = cls === "stopped" || cls === "failed";
  const scopeKind = unit.scopeKind || (unit.scope === "system" ? "rootful" : "rootless");

  async function action(act: string, ev: React.MouseEvent) {
    ev.preventDefault();
    ev.stopPropagation();
    if (acting) return;
    if (act === "stop" && cls === "running" && !confirm(`Stop ${unit.name}?`)) return;
    setActing(act);
    try {
      await api(`/api/units/${encodeURIComponent(unit.scope)}/${encodeURIComponent(unit.name)}/action`, { method: "POST", body: JSON.stringify({ action: act }) });
      toast(`${act} ${unit.name}: ok`);
      onChanged();
    } catch (e) {
      toast(`${act} ${unit.name}: ${(e as Error).message}`, true);
    } finally {
      setActing("");
    }
  }

  return (
    <Link to={`/unit/${encodeURIComponent(unit.scope)}/${encodeURIComponent(unit.name)}`} className={`unit-row ${cls} ${compact ? "is-compact" : ""}`}>
      {/* one glyph carries both kind and state: the type icon, colored by state */}
      <span className={`state-icon ${cls}`} title={`${unit.kind} · ${cls}`} aria-label={`${unit.kind} ${cls}`}><KindIcon kind={unit.kind} size={17} /></span>
      <span className="unit-main">
        <span className="unit-title">{displayName(unit.name)}</span>
        {!compact && <span className="unit-sub">{unit.description || unit.image || unit.path || ""}</span>}
      </span>
      {/* badges are right-aligned, so DOM is reversed to read right-to-left:
          node, privilege, pod, cpu, gpu, health, restarts */}
      <span className="badges">
        {!!unit.restarts && <RowChip icon={RotateCcw} color="var(--warn)" label="restarts">restarted {unit.restarts}×</RowChip>}
        {unit.health && <RowChip icon={Activity} color={unit.health === "unhealthy" ? "var(--bad)" : unit.health === "healthy" ? "var(--ok)" : "var(--warn)"} label="health">{unit.health}</RowChip>}
        {!!unit.gpus?.length && <RowChip icon={Gpu} tone="gpu" label="gpu">{unit.gpus.join(", ")}</RowChip>}
        {unit.stats && <RowChip icon={Cpu} label="usage">cpu {(unit.stats.cpuPct || 0).toFixed(1)}%{unit.stats.memBytes ? ` · mem ${fmtBytes(unit.stats.memBytes)}` : ""}</RowChip>}
        {unit.pod && <RowChip icon={Boxes} label={`pod ${unit.pod}`}>
          <button type="button" className="pop-link" onClick={(e) => { e.preventDefault(); e.stopPropagation(); navigate(`/unit/${encodeURIComponent(unit.scope)}/${encodeURIComponent(unit.pod!)}`); }}>{displayName(unit.pod)}</button>
        </RowChip>}
        <RowChip icon={scopeKind === "rootful" ? Shield : UserRound} tone={`priv-${scopeKind}`} label={scopeKind} />
        {unit.node && <RowChip icon={Server} color={nodeCol(unit.node)} label={`node ${unit.node}`}>node <b>{unit.node}</b> · {unit.scope}{unit.scopeUser ? ` (${unit.scopeUser})` : ""}</RowChip>}
      </span>
      {!auth.readOnly && (
        <span className="row-actions">
          {canStart && <button className="btn icon-only" disabled={!!acting} title="Start" onClick={(e) => action("start", e)}>{acting === "start" ? <RefreshCw className="spin" size={16} /> : <Play size={16} />}</button>}
          {(cls === "running" || cls === "pending") && <button className="btn icon-only" disabled={!!acting} title="Stop" onClick={(e) => action("stop", e)}>{acting === "stop" ? <RefreshCw className="spin" size={16} /> : <CircleStop size={16} />}</button>}
          <button className="btn icon-only" disabled={!!acting} title="Restart" onClick={(e) => action("restart", e)}>{acting === "restart" ? <RefreshCw className="spin" size={16} /> : <RefreshCw size={16} />}</button>
        </span>
      )}
    </Link>
  );
}

function UnitDetail() {
  const { scope = "", name = "" } = useParams();
  const [search] = useSearchParams();
  const initialTab = search.get("tab") || "overview";
  const api = useApi();
  const { auth, toast } = useApiContext();
  const navigate = useNavigate();
  const [tab, setTab] = useState(initialTab);
  const [unit, setUnit] = useState<Unit | null>(null);
  const [content, setContent] = useState("");
  const [savedContent, setSavedContent] = useState("");
  const [validation, setValidation] = useState<{ validation?: ValidationResult; hints?: string[] } | null>(null);
  const [error, setError] = useState("");
  const [members, setMembers] = useState<Unit[]>([]);
  const [acting, setActing] = useState("");
  const cls = unit ? stateClass(unit) : "unknown";
  const dirtyRef = useRef(false);
  useEffect(() => {
    dirtyRef.current = content !== savedContent;
  }, [content, savedContent]);

  const load = useCallback(async (opts?: { preserveDirty?: boolean }) => {
    try {
      const { body } = await api<{ unit: Unit; content: string }>(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`);
      setUnit(body.unit);
      const preserveDirty = opts?.preserveDirty ?? dirtyRef.current;
      if (!preserveDirty) {
        setContent(body.content);
        setSavedContent(body.content);
      }
      setError("");
      if (body.unit.kind === "pod") {
        const all = await api<{ units?: Unit[] }>("/api/units");
        setMembers((all.body.units || []).filter((u) => u.scope === scope && u.pod === name));
      }
    } catch (e) {
      setError((e as Error).message);
    }
  }, [api, name, scope]);

  useEffect(() => { load(); }, [load]);

  const refreshUnitState = useCallback(async () => {
    try {
      const { body } = await api<{ unit: Unit; content: string }>(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`);
      setUnit(body.unit);
      if (body.unit.kind === "pod") {
        const all = await api<{ units?: Unit[] }>("/api/units");
        setMembers((all.body.units || []).filter((u) => u.scope === scope && u.pod === name));
      }
    } catch {
      // Keep the last visible state; explicit actions still surface errors.
    }
  }, [api, name, scope]);

  useEffect(() => {
    const id = window.setInterval(() => {
      if (!document.hidden) refreshUnitState();
    }, 5000);
    return () => window.clearInterval(id);
  }, [refreshUnitState]);

  async function lifecycle(action: string) {
    if (!unit) return;
    if (acting) return;
    if (action === "stop" && cls === "running" && !confirm(`Stop ${unit.name}?`)) return;
    setActing(action);
    try {
      await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/action`, { method: "POST", body: JSON.stringify({ action }) });
      toast(`${action}: ok`);
      load();
    } catch (e) {
      toast(`${action}: ${(e as Error).message}`, true);
    } finally {
      setActing("");
    }
  }

  async function deleteUnit() {
    if (!unit || !confirm(`Delete ${name}? The unit file is removed from disk and ${unit.service} is stopped.`)) return;
    try {
      await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`, { method: "DELETE" });
      toast(`deleted ${name}`);
      navigate("/units");
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  async function validate() {
    try {
      const { body } = await api<{ validation?: ValidationResult; hints?: string[] }>("/api/validate", { method: "POST", body: JSON.stringify({ scope, name, content }) });
      setValidation(body);
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  async function save(restart: boolean) {
    try {
      const { status, body } = await api<{ validation?: ValidationResult; hints?: string[]; warnings?: string[] }>(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`, {
        method: "PUT",
        body: JSON.stringify({ content, restart, baseContent: savedContent }),
      });
      setValidation(body);
      if (status === 422) {
        toast("rejected by validator", true);
        return;
      }
      setSavedContent(content);
      (body.warnings || []).forEach((warning) => toast(warning, true));
      toast(`saved ${name} + daemon-reload`);
      load();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  if (error) return <Page title="Unit not available"><p className="banner banner-error">{error}</p></Page>;
  if (!unit) return <Page title="Unit"><p className="muted">Loading unit...</p></Page>;

  const changed = content !== savedContent;
  const scopeKind = unit.scopeKind || (unit.scope === "system" ? "rootful" : "rootless");
  return (
    <Page
      title={unit.name}
      kicker={`${unit.kind} · ${scopeKind} · ${unit.scope}`}
      back={<Link className="btn icon-only" to={viewForKind(unit.kind)}><ChevronLeft size={18} /></Link>}
      action={!auth.readOnly && <div className="action-row"><button className="btn" disabled={!!acting} onClick={() => lifecycle("start")}>{acting === "start" ? <RefreshCw className="spin" size={16} /> : <Play size={16} />} Start</button><button className="btn" disabled={!!acting} onClick={() => lifecycle("stop")}>{acting === "stop" ? <RefreshCw className="spin" size={16} /> : <CircleStop size={16} />} Stop</button><button className="btn" disabled={!!acting} onClick={() => lifecycle("restart")}>{acting === "restart" ? <RefreshCw className="spin" size={16} /> : <RefreshCw size={16} />} Restart</button></div>}
    >
      <div className="detail-summary">
        <StatusBadge state={cls} label={stateLabel(unit)} />
        {unit.unitFile && <span className="muted">{unit.unitFile}</span>}
        {unit.path && <code>{unit.path}</code>}
        {unit.readOnly && <span className="badge badge-warn">read-only file</span>}
      </div>
      <div className="tabs">
        {["overview", "editor", "logs", "history", ...(unit.kind === "pod" ? ["members"] : []), "actions"].map((t) => (
          <button key={t} className={`tab ${tab === t ? "active" : ""}`} onClick={() => setTab(t)}>{tabIcon(t)} {t}</button>
        ))}
      </div>
      {tab === "overview" && <OverviewTab unit={unit} members={members} />}
      {tab === "editor" && (
        <EditorTab
          unit={unit}
          content={content}
          setContent={setContent}
          savedContent={savedContent}
          validation={validation}
          changed={changed}
          onValidate={validate}
          onSave={save}
        />
      )}
      {tab === "logs" && <LogsTab scope={scope} name={name} aggregateMembers={unit.kind === "pod"} />}
      {tab === "history" && <HistoryTab scope={scope} name={name} currentContent={content} reload={load} />}
      {tab === "members" && <Panel title="Pod members" icon={Boxes}>{members.length ? members.map((u) => <UnitRow key={`${u.scope}/${u.name}`} unit={u} onChanged={load} />) : <p className="muted">No container units declare Pod={name} yet.</p>}</Panel>}
      {tab === "actions" && <ActionsTab unit={unit} onAction={lifecycle} onDelete={deleteUnit} />}
    </Page>
  );
}

function tabIcon(tab: string) {
  const map: Record<string, React.ReactNode> = {
    overview: <Activity size={15} />,
    editor: <SquarePen size={15} />,
    logs: <Logs size={15} />,
    history: <FileClock size={15} />,
    members: <Boxes size={15} />,
    actions: <Settings size={15} />,
  };
  return map[tab];
}

function OverviewTab({ unit, members }: { unit: Unit; members: Unit[] }) {
  return (
    <div className="detail-grid">
      <Panel title="Runtime" icon={Activity}>
        <InfoGrid rows={[
          ["State", stateLabel(unit)],
          ["Kind", unit.kind],
          ["Scope", unit.scope],
          ["Scope type", unit.scopeKind || (unit.scope === "system" ? "rootful" : "rootless")],
          ["Scope user", unit.scopeUser || "n/a"],
          ["Image", unit.image || "n/a"],
          ["Restarts", String(unit.restarts || 0)],
          ["Health", unit.health || "n/a"],
          ["CPU", unit.stats ? `${(unit.stats.cpuPct || 0).toFixed(1)}%` : "n/a"],
          ["Memory", unit.stats?.memBytes ? fmtBytes(unit.stats.memBytes) : "n/a"],
          ["Pod", unit.pod || "n/a"],
        ]} />
      </Panel>
      <Panel title="Attachments" icon={Network}>
        <InfoGrid rows={[
          ["GPU", unit.gpus?.join(", ") || "none"],
          ["Unit file", unit.unitFile || "n/a"],
          ["Path", unit.path || "n/a"],
          ["Members", members.length ? String(members.length) : "n/a"],
        ]} />
      </Panel>
    </div>
  );
}

function EditorTab({ unit, content, setContent, savedContent, validation, changed, onValidate, onSave }: {
  unit: Unit;
  content: string;
  setContent: (v: string) => void;
  savedContent: string;
  validation: { validation?: ValidationResult; hints?: string[] } | null;
  changed: boolean;
  onValidate: () => void;
  onSave: (restart: boolean) => void;
}) {
  const { auth } = useApiContext();
  const [restart, setRestart] = useState(false);
  const [review, setReview] = useState(false);
  const readOnly = auth.readOnly || unit.readOnly;
  useDirtyGuard(changed);
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "s" && !readOnly) {
        ev.preventDefault();
        if (changed) setReview(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [changed, readOnly]);
  const gpuSnippets: Record<string, string[]> = {
    nvidia: ["AddDevice=nvidia.com/gpu=all"],
    vaapi: ["AddDevice=/dev/dri"],
    rocm: ["AddDevice=/dev/dri", "AddDevice=/dev/kfd"],
  };

  return (
    <Panel title="Unit file" icon={SquarePen}>
      <div className="editor-actions">
        <button className="btn" onClick={onValidate}><Check size={16} /> Validate</button>
        {!readOnly && <button className="btn btn-accent" disabled={!changed} onClick={() => setReview(true)}><Save size={16} /> Save + reload</button>}
        {!readOnly && <label className="check"><input type="checkbox" checked={restart} onChange={(e) => setRestart(e.target.checked)} /> restart after save</label>}
        {!readOnly && unit.kind === "container" && (
          <select className="input" defaultValue="" onChange={(e) => { const lines = gpuSnippets[e.target.value]; e.currentTarget.value = ""; if (lines) setContent(insertIntoSection(content, "Container", lines)); }}>
            <option value="">Add GPU...</option>
            <option value="nvidia">NVIDIA CDI</option>
            <option value="vaapi">Intel/AMD VAAPI</option>
            <option value="rocm">AMD ROCm</option>
          </select>
        )}
      </div>
      <CodeEditor value={content} onChange={setContent} readOnly={readOnly} onSave={() => changed && setReview(true)} />
      {validation && <ValidationBlock validation={validation.validation} hints={validation.hints || []} />}
      {review && (
        <div className="review-box">
          <div className="review-head"><b>Review changes</b><button className="btn icon-only" onClick={() => setReview(false)}><X size={16} /></button></div>
          <DiffView before={savedContent} after={content} />
          <button className="btn btn-accent" onClick={() => { setReview(false); onSave(restart); }}>Confirm save + reload</button>
        </div>
      )}
    </Panel>
  );
}

function ValidationBlock({ validation, hints }: { validation?: ValidationResult; hints: string[] }) {
  return (
    <div className="validation">
      {validation && <pre className={`output ${validation.valid ? "ok" : "err"}`}>{`${validation.available ? (validation.valid ? "valid" : "invalid") : "validator unavailable"}${validation.output ? `\n\n${validation.output}` : ""}`}</pre>}
      {hints.map((h) => <p key={h} className="banner banner-warn">{h}</p>)}
    </div>
  );
}

function DiffView({ before, after }: { before: string; after: string }) {
  return <pre className="output diff">{lineDiff(before, after).map(([op, line], i) => <span key={i} className={op === "+" ? "diff-add" : op === "-" ? "diff-del" : "diff-ctx"}>{op} {line}{"\n"}</span>)}</pre>;
}

function LogsTab({ scope, name, aggregateMembers = false }: { scope: string; name: string; aggregateMembers?: boolean }) {
  const [follow, setFollow] = useState(true);
  const [tail, setTail] = useState(200);
  const [since, setSince] = useState("");
  const [showTimestamps, setShowTimestamps] = useState(true);
  const [filter, setFilter] = useState("");
  const [lines, setLines] = useState<string[]>([]);
  const [streamState, setStreamState] = useState("connecting");
  const ref = useRef<HTMLPreElement | null>(null);

  useEffect(() => {
    let src: EventSource | null = null;
    let timer = 0;
    let backoff = 1000;
    let cancelled = false;
    setLines([]);
    function connect() {
      if (cancelled) return;
      setStreamState("connecting");
      const q = new URLSearchParams({ follow: follow ? "1" : "0", lines: String(tail) });
      if (since.trim()) q.set("since", since.trim());
      if (aggregateMembers) q.set("members", "1");
      src = new EventSource(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/logs?${q}`);
      src.onopen = () => {
        setStreamState(follow ? "live" : "loaded");
        backoff = 1000;
      };
      src.onmessage = (ev) => {
        let line = ev.data;
        try {
          const j = JSON.parse(ev.data);
          const ts = j.__REALTIME_TIMESTAMP ? new Date(Number(j.__REALTIME_TIMESTAMP) / 1000).toLocaleString() : "";
          const msg = typeof j.MESSAGE === "string" ? j.MESSAGE : JSON.stringify(j.MESSAGE);
          line = showTimestamps && ts ? `${ts}  ${msg}` : msg;
        } catch {
          // Show raw line.
        }
        setLines((prev) => [...prev, line].slice(-5000));
      };
      src.onerror = () => {
        src?.close();
        if (!follow || cancelled) {
          setStreamState("loaded");
          return;
        }
        setStreamState("stream lost - reconnecting");
        timer = window.setTimeout(connect, backoff);
        backoff = Math.min(backoff * 2, 15000);
      };
    }
    connect();
    return () => {
      cancelled = true;
      src?.close();
      window.clearTimeout(timer);
    };
  }, [aggregateMembers, follow, name, scope, showTimestamps, since, tail]);

  useEffect(() => {
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [lines]);

  const needle = filter.trim().toLowerCase();
  const visible = useMemo(() => {
    return needle ? lines.filter((line) => line.toLowerCase().includes(needle)) : lines;
  }, [needle, lines]);
  const text = visible.join("\n") + (visible.length ? "\n" : "");

  function download() {
    const blob = new Blob([text], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${name}.log`;
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <Panel title="Logs" icon={Logs} action={<span className="badge">{streamState}</span>}>
      <div className="filterbar">
        <label className="searchbox"><Search size={16} /><input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter logs..." /></label>
        <select className="input" value={tail} onChange={(e) => setTail(Number(e.target.value))}><option value={200}>200 lines</option><option value={1000}>1000 lines</option><option value={5000}>5000 lines</option></select>
        <input className="input" placeholder="since, e.g. 1 hour ago" value={since} onChange={(e) => setSince(e.target.value)} />
        <label className="check"><input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> follow</label>
        <label className="check"><input type="checkbox" checked={showTimestamps} onChange={(e) => setShowTimestamps(e.target.checked)} /> timestamps</label>
        <button className="btn btn-sm" onClick={() => navigator.clipboard.writeText(text)}><Check size={14} /> Copy</button>
        <button className="btn btn-sm" onClick={download}><Download size={14} /> Download</button>
      </div>
      <pre ref={ref} className="output logs-output">{needle && visible.length ? visible.map((line, i) => <span key={i}>{highlightMatches(line, needle)}{"\n"}</span>) : (text || "connecting...")}</pre>
    </Panel>
  );
}

function HistoryTab({ scope, name, currentContent, reload }: { scope: string; name: string; currentContent: string; reload: () => void }) {
  const api = useApi();
  const { toast } = useApiContext();
  const [enabled, setEnabled] = useState(false);
  const [commits, setCommits] = useState<Array<{ hash: string; time: number; subject: string }>>([]);
  const [message, setMessage] = useState("Loading history...");
  const [diff, setDiff] = useState<React.ReactNode>(null);

  const load = useCallback(async () => {
    try {
      const { body } = await api<{ enabled?: boolean; commits?: Array<{ hash: string; time: number; subject: string }> }>(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/history`);
      setEnabled(!!body.enabled);
      setCommits(body.commits || []);
      setMessage(body.enabled ? "" : "Git history is off for this scope.");
    } catch (e) {
      setMessage((e as Error).message);
    }
  }, [api, name, scope]);

  useEffect(() => { load(); }, [load]);

  async function showDiff(hash: string) {
    try {
      const { body } = await api<{ content: string }>(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/history/${hash}`);
      setDiff(<DiffView before={currentContent} after={body.content} />);
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  async function restore(hash: string) {
    if (!confirm(`Restore ${name} to ${hash.slice(0, 10)}?`)) return;
    try {
      const { status } = await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/rollback`, { method: "POST", body: JSON.stringify({ commit: hash }) });
      if (status === 422) {
        toast("rollback rejected: that revision no longer validates on this host", true);
        return;
      }
      toast(`restored ${name} to ${hash.slice(0, 10)}`);
      reload();
      load();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  return (
    <Panel title="History" icon={FileClock}>
      {message && <p className="muted">{message}</p>}
      {enabled && !commits.length && <p className="muted">No commits for this unit yet.</p>}
      {commits.map((c) => (
        <div className="history-row" key={c.hash}>
          <code>{c.hash.slice(0, 10)}</code>
          <span className="muted">{new Date(c.time * 1000).toLocaleString()}</span>
          <span className="grow">{c.subject}</span>
          <button className="btn btn-sm" onClick={() => showDiff(c.hash)}>diff</button>
          <button className="btn btn-sm" onClick={() => restore(c.hash)}>restore</button>
        </div>
      ))}
      {diff}
    </Panel>
  );
}

function ActionsTab({ unit, onAction, onDelete }: { unit: Unit; onAction: (action: string) => void; onDelete: () => void }) {
  const { auth } = useApiContext();
  if (auth.readOnly) return <Panel title="Actions" icon={Settings}><p className="muted">This session is read-only.</p></Panel>;
  return (
    <Panel title="Actions" icon={Settings}>
      <div className="action-grid">
        {["start", "stop", "restart", "enable", "disable"].map((a) => <button className="btn" key={a} onClick={() => onAction(a)}>{actionIcon(a)} {a}</button>)}
        <button className="btn btn-danger" onClick={onDelete}><Trash2 size={16} /> delete {unit.name}</button>
      </div>
    </Panel>
  );
}

function actionIcon(action: string) {
  const icons: Record<string, React.ReactNode> = {
    start: <Play size={16} />,
    stop: <CircleStop size={16} />,
    restart: <RefreshCw size={16} />,
    enable: <Check size={16} />,
    disable: <X size={16} />,
  };
  return icons[action] || <Zap size={16} />;
}

function FleetView() {
  const api = useApi();
  const { auth, toast } = useApiContext();
  const [nodes, setNodes] = useState<ManagedNode[]>([]);
  const [groups, setGroups] = useState<NodeGroup[]>([]);
  const [license, setLicense] = useState<LicenseStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [newNodeID, setNewNodeID] = useState("");
  const [newNodeTarget, setNewNodeTarget] = useState("");
  const [q, setQ] = useState("");
  const [params, setParams] = useSearchParams();
  const addOpen = auth.role === "admin" && !auth.readOnly && params.get("new") === "1";
  function closeAdd() { const next = new URLSearchParams(params); next.delete("new"); setParams(next, { replace: true }); }
  const load = useCallback(async () => {
    try {
      const { body } = await api<{ nodes?: ManagedNode[]; license?: LicenseStatus }>("/api/nodes");
      setNodes(body.nodes || []);
      setLicense(body.license || null);
      const groupResp = await api<{ groups?: NodeGroup[] }>("/api/groups");
      setGroups(groupResp.body.groups || []);
    } catch (e) {
      toast((e as Error).message, true);
    } finally {
      setLoading(false);
    }
  }, [api, toast]);
  useEffect(() => { load(); }, [load]);
  const totalUnits = nodes.reduce((sum, n) => sum + n.units, 0);
  const failed = nodes.reduce((sum, n) => sum + n.failed, 0);
  const filteredNodes = nodes.filter((node) => {
    const needle = q.trim().toLowerCase();
    return !needle || `${node.id} ${node.address || ""} ${(node.labels || []).join(" ")} ${node.errors?.join(" ") || ""}`.toLowerCase().includes(needle);
  });
  async function addNode() {
    try {
      await api("/api/nodes", { method: "POST", body: JSON.stringify({ id: newNodeID, target: newNodeTarget }) });
      setNewNodeID("");
      setNewNodeTarget("");
      toast("remote node added");
      closeAdd();
      load();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  return (
    <Page title="Fleet" kicker="Managed Podman nodes">
      <div className="tiles">
        {/* X/Y already conveys remaining and over-limit; the warn tone flags it. */}
        <MetricTile label="managed nodes" value={license ? `${license.managedNodes}/${license.nodeLimit}` : nodes.length} tone={license && !license.enterpriseAvailable ? "warn" : "ok"} />
        <MetricTile label="units" value={totalUnits} tone={totalUnits ? "ok" : "dim"} />
        <MetricTile label="failed" value={failed} tone={failed ? "bad" : "dim"} />
        <MetricTile label={license ? `${license.edition}${license.enterpriseAvailable ? "" : " (free)"}` : "unknown"} value="Edition" tone="dim" />
      </div>
      {license?.message && <p className={`banner ${license.enterpriseAvailable ? "" : "banner-warn"}`}>{license.message}</p>}
      {addOpen && <Overlay title="Add remote node" onClose={closeAdd}>
        <div className="stack-form">
          <label className="stack-field"><span>Node ID</span><input className="input" placeholder="e.g. nas" value={newNodeID} onChange={(e) => setNewNodeID(e.target.value)} /></label>
          <label className="stack-field"><span>SSH target</span><input className="input" placeholder="e.g. root@nas.local" value={newNodeTarget} onChange={(e) => setNewNodeTarget(e.target.value)} /></label>
          <button className="btn btn-accent" disabled={!newNodeID || !newNodeTarget} onClick={addNode}><Plus size={16} /> Add node</button>
        </div>
      </Overlay>}
      <Panel title="Nodes" icon={Network}>
        <div className="filterbar node-filterbar"><label className="searchbox"><Search size={16} /><input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search nodes..." /></label></div>
        {loading ? <p className="muted">Loading nodes...</p> : filteredNodes.length ? filteredNodes.map((node) => <NodeRow key={node.id} node={node} editable={auth.role === "admin" && !auth.readOnly} onChanged={load} />) : <p className="muted">No matching nodes.</p>}
      </Panel>
      <Panel title="Groups" icon={ListFilter}>
        {groups.length ? groups.map((group) => <div className="history-row" key={group.label}>
          <div><strong>{group.label}</strong><div className="muted">{group.nodes.join(", ")}</div></div>
          <span className="grow" />
          <span className="badge">{group.nodes.length} nodes</span>
          <span className="badge">{group.units} units</span>
          <span className="badge badge-running">{group.running} running</span>
          {group.failed > 0 && <span className="badge badge-failed">{group.failed} failed</span>}
        </div>) : <p className="muted">Add labels to nodes to create groups.</p>}
      </Panel>
    </Page>
  );
}

function NodeRow({ node, editable, onChanged }: { node: ManagedNode; editable?: boolean; onChanged: () => void }) {
  const api = useApi();
  const { toast } = useApiContext();
  const { setSel } = useNodeSel();
  const navigate = useNavigate();
  const [labelsOpen, setLabelsOpen] = useState(false);
  const [detailOpen, setDetailOpen] = useState(false);
  const [labelDraft, setLabelDraft] = useState(node.labels?.join(", ") || "");
  const [nameDraft, setNameDraft] = useState(node.displayName || "");
  const [colorDraft, setColorDraft] = useState(node.color || nodeColor(node.id));
  const scopeText = node.scopes.map((s) => s.system ? `${s.label} (rootful)` : `${s.label} (${s.user || "user"} rootless)`).join(", ");
  const rootful = node.rootful || { units: 0, running: 0, failed: 0, unknown: 0 };
  const rootless = node.rootless || { units: 0, running: 0, failed: 0, unknown: 0 };
  const memPct = node.metrics?.memTotalKb ? Math.round(100 * (1 - (node.metrics.memAvailKb || 0) / node.metrics.memTotalKb)) : null;
  async function saveLabels() {
    try {
      await api(`/api/nodes/${encodeURIComponent(node.id)}/labels`, { method: "PATCH", body: JSON.stringify({ labels: labelDraft.split(",") }) });
      await api(`/api/nodes/${encodeURIComponent(node.id)}/appearance`, { method: "PATCH", body: JSON.stringify({ color: colorDraft === nodeColor(node.id) ? "" : colorDraft, displayName: nameDraft.trim() }) });
      toast("node updated");
      setLabelsOpen(false);
      onChanged();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  async function removeNode() {
    if (!confirm(`Remove remote node ${node.id}?`)) return;
    try {
      await api(`/api/nodes/${encodeURIComponent(node.id)}`, { method: "DELETE" });
      toast("remote node removed");
      onChanged();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  return (
    <div className="history-row node-row">
      <div className="node-click" onClick={() => setDetailOpen(true)}>
        <div className="node-id-block">
          <div><span className="node-swatch" style={{ background: node.color || nodeColor(node.id) }} /><strong>{node.displayName || (node.local ? "local" : node.id)}</strong>{node.metrics?.hostname && node.metrics.hostname !== node.id && <span className="muted"> · {node.metrics.hostname}</span>}</div>
          <div className="muted node-meta">{[node.metrics?.kernel, node.metrics?.cores != null ? `${node.metrics.cores} cores` : null, node.metrics?.memTotalKb ? `${fmtBytes(node.metrics.memTotalKb * 1024)} RAM` : null].filter(Boolean).join(" · ") || scopeText || "no scopes"}</div>
          {!!node.labels?.length && <div>{node.labels.map((label) => <span className="badge badge-user" key={label}>{label}</span>)}</div>}
          {node.errors?.length ? <div className="warn-text">{node.errors.join("; ")}</div> : null}
        </div>
        <span className="grow" />
        <span className="badges">
          <RowChip icon={Gauge} label="usage">{[node.metrics?.cpuPct != null && node.metrics.cpuPct >= 0 ? `cpu ${node.metrics.cpuPct}%` : null, memPct != null ? `mem ${memPct}%` : null, node.metrics?.load1 != null ? `load ${node.metrics.load1.toFixed(2)}` : null].filter(Boolean).join(" · ") || "no metrics"}</RowChip>
          {rootful.units > 0 && <RowChip icon={Shield} tone="priv-rootful" color={rootful.failed ? "var(--bad)" : undefined} label="rootful">rootful {rootful.running}/{rootful.units} running{rootful.failed ? `, ${rootful.failed} failed` : ""}</RowChip>}
          {rootless.units > 0 && <RowChip icon={UserRound} tone="priv-rootless" color={rootless.failed ? "var(--bad)" : undefined} label="rootless">rootless {rootless.running}/{rootless.units} running{rootless.failed ? `, ${rootless.failed} failed` : ""}</RowChip>}
        </span>
      </div>
      <button className="btn btn-sm" onClick={(e) => { e.stopPropagation(); setSel(node.id); navigate("/"); }}><Gauge size={14} /> manage</button>
      {editable && <button className="btn btn-sm" onClick={(e) => { e.stopPropagation(); setLabelDraft(node.labels?.join(", ") || ""); setNameDraft(node.displayName || ""); setColorDraft(node.color || nodeColor(node.id)); setLabelsOpen(true); }}><SquarePen size={14} /> edit</button>}
      {editable && !node.local && !!node.address && <button className="btn btn-sm btn-danger" onClick={(e) => { e.stopPropagation(); removeNode(); }}><Trash2 size={14} /> remove</button>}
      {labelsOpen && <Overlay title={`Edit ${node.local ? "local" : node.id}`} onClose={() => setLabelsOpen(false)}>
        <div className="stack-form">
          <label className="wizard-field"><span>Display name</span><input className="input" value={nameDraft} onChange={(e) => setNameDraft(e.target.value)} placeholder={node.local ? "local" : node.id} /></label>
          <label className="wizard-field"><span>Color</span><span className="node-color-pick"><input type="color" value={colorDraft} onChange={(e) => setColorDraft(e.target.value)} /><button type="button" className="btn btn-sm" onClick={() => setColorDraft(nodeColor(node.id))}>reset to auto</button></span></label>
          <label className="wizard-field"><span>Labels <span className="muted">(comma-separated)</span></span><input className="input" value={labelDraft} onChange={(e) => setLabelDraft(e.target.value)} placeholder="prod, gpu" /></label>
          <button className="btn btn-accent" onClick={saveLabels}><Save size={16} /> Save</button>
        </div>
      </Overlay>}
      {detailOpen && <NodeDetail node={node} onClose={() => setDetailOpen(false)} />}
    </div>
  );
}

function NodeDetail({ node, onClose }: { node: ManagedNode; onClose: () => void }) {
  const m = node.metrics;
  const memPct = m?.memTotalKb ? Math.round(100 * (1 - (m.memAvailKb || 0) / m.memTotalKb)) : null;
  const scopeLine = (c?: NodeCounts) => c ? `${c.running}/${c.units} running${c.failed ? `, ${c.failed} failed` : ""}` : null;
  return (
    <Overlay title={node.local ? "local host" : node.id} onClose={onClose}>
      <div className="tiles">
        {m?.cpuPct != null && m.cpuPct >= 0 && <MetricTile label="cpu" value={`${m.cpuPct}%`} tone="dim" />}
        {memPct != null && <MetricTile label="mem" value={`${memPct}%`} tone="dim" />}
        {m?.load1 != null && <MetricTile label="load" value={m.load1.toFixed(2)} tone="dim" />}
        <MetricTile label="units" value={node.units} tone={node.units ? "ok" : "dim"} />
        {node.failed > 0 && <MetricTile label="failed" value={node.failed} tone="bad" />}
      </div>
      <dl className="kv">
        {m?.hostname && <><dt>hostname</dt><dd>{m.hostname}</dd></>}
        {m?.kernel && <><dt>kernel</dt><dd>{m.kernel}</dd></>}
        {m?.cores != null && <><dt>cores</dt><dd>{m.cores}</dd></>}
        {m?.memTotalKb ? <><dt>memory</dt><dd>{fmtBytes(m.memTotalKb * 1024)}</dd></> : null}
        {node.address && <><dt>address</dt><dd>{node.address}</dd></>}
        <dt>scopes</dt><dd>{node.scopes.map((s) => s.system ? `${s.label} (rootful)` : `${s.label} (${s.user || "user"} rootless)`).join(", ") || "none"}</dd>
        {scopeLine(node.rootful) && <><dt>rootful</dt><dd>{scopeLine(node.rootful)}</dd></>}
        {scopeLine(node.rootless) && <><dt>rootless</dt><dd>{scopeLine(node.rootless)}</dd></>}
        {!!node.labels?.length && <><dt>labels</dt><dd>{node.labels.join(", ")}</dd></>}
        {!!node.unitDirs?.length && <><dt>unit dirs</dt><dd>{node.unitDirs.join(", ")}</dd></>}
      </dl>
      {node.errors?.length ? <p className="warn-text">{node.errors.join("; ")}</p> : null}
    </Overlay>
  );
}

function PoliciesView() {
  const api = useApi();
  const { auth, toast } = useApiContext();
  const [findings, setFindings] = useState<PolicyFinding[]>([]);
  const [loading, setLoading] = useState(true);
  const [severity, setSeverity] = useState("all");
  const [visibility, setVisibility] = useState<"active" | "all" | "waived">("active");
  const [q, setQ] = useState("");
  const load = useCallback(async () => {
    try {
      const { body } = await api<{ findings?: PolicyFinding[] }>("/api/policies");
      setFindings(body.findings || []);
    } catch (e) {
      toast((e as Error).message, true);
    } finally {
      setLoading(false);
    }
  }, [api, toast]);
  useEffect(() => { load(); }, [load]);
  const severityOf = (finding: PolicyFinding) => finding.severity.trim().toLowerCase();
  const active = findings.filter((f) => !f.waived);
  const critical = active.filter((f) => severityOf(f) === "critical").length;
  const warn = active.filter((f) => severityOf(f) === "warn").length;
  const waived = findings.length - active.length;
  const countSource = visibility === "active" ? active : visibility === "waived" ? findings.filter((f) => f.waived) : findings;
  const severityOptions = useMemo(() => {
    const seen = new Set(["all", "critical", "warn", "info"]);
    countSource.forEach((finding) => {
      const sev = severityOf(finding);
      if (sev) seen.add(sev);
    });
    return Array.from(seen);
  }, [countSource]);
  const severityCounts = useMemo(() => {
    const counts: Record<string, number> = { all: countSource.length, critical: 0, warn: 0, info: 0 };
    countSource.forEach((f) => {
      const sev = severityOf(f);
      counts[sev] = (counts[sev] || 0) + 1;
    });
    return counts;
  }, [countSource]);
  useEffect(() => {
    if (!severityOptions.includes(severity)) setSeverity("all");
  }, [severity, severityOptions]);
  const filtered = countSource.filter((finding) => {
    const needle = q.trim().toLowerCase();
    if (severity !== "all" && severityOf(finding) !== severity) return false;
    return !needle || `${finding.policy} ${finding.node} ${finding.scope} ${finding.unit || ""} ${finding.message} ${finding.waiverReason || ""}`.toLowerCase().includes(needle);
  });
  return (
    <Page title="Policy" kicker="Fleet checks">
      <div className="tiles">
        <MetricTile label="active findings" value={active.length} tone={active.length ? "warn" : "dim"} />
        <MetricTile label="critical" value={critical} tone={critical ? "bad" : "dim"} />
        <MetricTile label="warnings" value={warn} tone={warn ? "warn" : "dim"} />
        <MetricTile label="waived" value={waived} tone="dim" />
      </div>
      <Panel title={`Findings (${filtered.length}/${countSource.length})`} icon={Shield}>
        <div className="filterbar">
          <label className="searchbox"><Search size={16} /><input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search findings..." /></label>
          <label className="field-label">Status<select className="input" value={visibility} onChange={(e) => setVisibility(e.target.value as "active" | "all" | "waived")}>
            <option value="active">active ({active.length})</option>
            <option value="all">all ({findings.length})</option>
            <option value="waived">waived ({waived})</option>
          </select></label>
          <label className="field-label">Severity<select className="input" value={severity} onChange={(e) => setSeverity(e.target.value)}>
            {severityOptions.map((s) => <option key={s} value={s}>{s} ({severityCounts[s] || 0})</option>)}
          </select></label>
        </div>
        {/* finding.key alone is NOT unique: one unit emits several findings per
            policy (one per bind mount / image), all sharing one waiver key.
            Duplicate React keys made the list render stale/dropped rows whenever
            the filters changed — the filter logic itself was never the bug. */}
        {loading ? <p className="muted">Scanning Quadlet files...</p> : filtered.length ? filtered.map((finding) => <PolicyRow key={`${finding.key}:${finding.message}`} finding={finding} editable={auth.role === "admin" && !auth.readOnly} onChanged={load} />) : <EmptyState title="No matching findings" text="Adjust the policy filters or clear waivers." />}
      </Panel>
    </Page>
  );
}

function PolicyRow({ finding, editable, onChanged }: { finding: PolicyFinding; editable?: boolean; onChanged: () => void }) {
  const api = useApi();
  const { toast } = useApiContext();
  const [waiveOpen, setWaiveOpen] = useState(false);
  const [reason, setReason] = useState(finding.waiverReason || "");
  const badge = finding.severity === "critical" ? "badge-failed" : finding.severity === "warn" ? "badge-warn" : "";
  async function waive() {
    try {
      await api("/api/policies/waivers", { method: "POST", body: JSON.stringify({ key: finding.key, reason }) });
      toast("policy finding waived");
      setWaiveOpen(false);
      onChanged();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  async function unwaive() {
    try {
      await api(`/api/policies/waivers/${encodeURIComponent(finding.key)}`, { method: "DELETE" });
      toast("policy waiver removed");
      onChanged();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  return (
    <div className="history-row">
      <div>
        <strong>{finding.policy}</strong>
        <div className="muted">{finding.node} · {finding.scope}{finding.unit ? ` · ${finding.unit}` : ""}</div>
        <div>{finding.message}</div>
        {finding.waived && <div className="muted">waived{finding.waivedBy ? ` by ${finding.waivedBy}` : ""}{finding.waiverReason ? ` · ${finding.waiverReason}` : ""}</div>}
      </div>
      <span className="grow" />
      <span className={`badge ${badge}`}>{finding.severity}</span>
      {finding.waived && <span className="badge">waived</span>}
      {editable && (finding.waived ? <button className="btn btn-sm" onClick={unwaive}><X size={14} /> unwaive</button> : <button className="btn btn-sm" onClick={() => { setReason(finding.waiverReason || ""); setWaiveOpen(true); }}><Check size={14} /> waive</button>)}
      {waiveOpen && <Overlay title="Waive finding" onClose={() => setWaiveOpen(false)}>
        <div className="stack-form">
          <textarea className="input" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Reason" />
          <button className="btn btn-accent" onClick={waive}><Check size={16} /> Waive</button>
        </div>
      </Overlay>}
    </div>
  );
}

// buildQuadlet renders a minimal, valid Quadlet unit from wizard fields — shown
// live as the user types, so the wizard doubles as a way to learn the syntax.
function buildQuadlet(kind: string, f: { name: string; image: string; driver: string; subnet: string; ports: string[]; volumes: string[]; env: string[] }): string {
  const L: string[] = [];
  const list = (arr: string[]) => arr.map((x) => x.trim()).filter(Boolean);
  if (kind === "container") {
    L.push("[Unit]", `Description=${f.name.trim() || "container"}`, "", "[Container]");
    if (f.image.trim()) L.push(`Image=${f.image.trim()}`);
    list(f.ports).forEach((p) => L.push(`PublishPort=${p}`));
    list(f.volumes).forEach((v) => L.push(`Volume=${v}`));
    list(f.env).forEach((e) => L.push(`Environment=${e}`));
    L.push("", "[Service]", "Restart=always", "", "[Install]", "WantedBy=default.target");
  } else if (kind === "pod") {
    L.push("[Pod]");
    list(f.ports).forEach((p) => L.push(`PublishPort=${p}`));
    L.push("", "[Install]", "WantedBy=default.target");
  } else if (kind === "network") {
    L.push("[Network]");
    if (f.driver.trim()) L.push(`Driver=${f.driver.trim()}`);
    if (f.subnet.trim()) L.push(`Subnet=${f.subnet.trim()}`);
  } else if (kind === "volume") {
    L.push("[Volume]");
    if (f.driver.trim()) L.push(`Driver=${f.driver.trim()}`);
  }
  return L.join("\n") + "\n";
}

function WizardListInput({ label, placeholder, values, onChange }: { label: string; placeholder: string; values: string[]; onChange: (v: string[]) => void }) {
  return (
    <div className="wizard-field">
      <label>{label}</label>
      {values.map((v, i) => (
        <div className="wizard-listrow" key={i}>
          <input className="input" placeholder={placeholder} value={v} onChange={(e) => onChange(values.map((x, j) => (j === i ? e.target.value : x)))} />
          <button type="button" className="btn icon-only" title="Remove" onClick={() => onChange(values.filter((_, j) => j !== i))}><X size={16} /></button>
        </div>
      ))}
      <button type="button" className="btn btn-sm" onClick={() => onChange([...values, ""])}><Plus size={14} /> add {label.toLowerCase()}</button>
    </div>
  );
}

// Wizard: guided per-type create with a live Quadlet preview. Novices fill
// fields; advanced users can flip to the raw editor and keep what's generated.
function Wizard({ kind, onCreated }: { kind: string; onCreated?: () => void }) {
  const api = useApi();
  const { toast } = useApiContext();
  const navigate = useNavigate();
  const [scopes, setScopes] = useState(["system"]);
  const [scope, setScope] = useState("system");
  const [name, setName] = useState("");
  const [image, setImage] = useState("");
  const [driver, setDriver] = useState(kind === "network" ? "bridge" : "local");
  const [subnet, setSubnet] = useState("");
  const [ports, setPorts] = useState<string[]>([]);
  const [volumes, setVolumes] = useState<string[]>([]);
  const [env, setEnv] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api<HostInfo>("/api/host").then(({ body }) => { const s = body.scopes || ["system"]; setScopes(s); setScope(s[0] || "system"); }).catch(() => undefined);
  }, [api]);
  const content = buildQuadlet(kind, { name, image, driver, subnet, ports, volumes, env });
  async function create() {
    if (!name.trim()) { toast("name is required", true); return; }
    if (kind === "container" && !image.trim()) { toast("image is required", true); return; }
    setBusy(true);
    const unit = `${name.trim()}.${kind}`;
    try {
      const { status } = await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(unit)}`, { method: "PUT", body: JSON.stringify({ content }) });
      if (status === 422) { toast("rejected by validator", true); return; }
      toast(`created ${unit}`);
      onCreated?.();
      navigate(`/unit/${encodeURIComponent(scope)}/${encodeURIComponent(unit)}`);
    } catch (e) {
      toast((e as Error).message, true);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="wizard">
      <div className="wizard-form">
        <div className="wizard-field"><label>Server / scope</label><select className="input" value={scope} onChange={(e) => setScope(e.target.value)}>{scopes.map((s) => <option key={s}>{s}</option>)}</select></div>
        <div className="wizard-field"><label>Name</label><input className="input" placeholder={`my-${kind}`} value={name} onChange={(e) => setName(e.target.value)} /></div>
        {kind === "container" && <div className="wizard-field"><label>Image</label><input className="input" placeholder="docker.io/library/nginx:latest" value={image} onChange={(e) => setImage(e.target.value)} /></div>}
        {kind === "network" && <>
          <div className="wizard-field"><label>Driver</label><select className="input" value={driver} onChange={(e) => setDriver(e.target.value)}><option>bridge</option><option>macvlan</option><option>ipvlan</option></select></div>
          <div className="wizard-field"><label>Subnet <span className="muted">(optional — blank = auto)</span></label><input className="input" placeholder="10.89.0.0/24" value={subnet} onChange={(e) => setSubnet(e.target.value)} /></div>
        </>}
        {kind === "volume" && <div className="wizard-field"><label>Driver</label><input className="input" value={driver} onChange={(e) => setDriver(e.target.value)} /></div>}
        {(kind === "container" || kind === "pod") && <WizardListInput label="Ports" placeholder="8080:80" values={ports} onChange={setPorts} />}
        {kind === "container" && <WizardListInput label="Volumes" placeholder="/host:/data or myvol:/data" values={volumes} onChange={setVolumes} />}
        {kind === "container" && <WizardListInput label="Environment" placeholder="KEY=value" values={env} onChange={setEnv} />}
        <button className="btn btn-accent" disabled={busy} onClick={create}>{busy ? <RefreshCw className="spin" size={16} /> : <Plus size={16} />} Create {kind}</button>
      </div>
      <div className="wizard-preview">
        <div className="wizard-preview-head">{name.trim() || "unit"}.{kind}</div>
        <pre>{content}</pre>
      </div>
    </div>
  );
}

// CreateFlow is the "+ Add" body: a guided wizard by default, with a flip to
// the raw editor, and (for containers) a shortcut to the docker-run importer.
function CreateFlow({ kind, onCreated }: { kind: string; onCreated?: () => void }) {
  const [mode, setMode] = useState<"wizard" | "editor">("wizard");
  return (
    <div className="create-flow">
      <div className="create-mode">
        <div className="segmented text-segmented">
          <button className={mode === "wizard" ? "active" : ""} onClick={() => setMode("wizard")}>Guided wizard</button>
          <button className={mode === "editor" ? "active" : ""} onClick={() => setMode("editor")}>Editor</button>
        </div>
        {kind === "container" && <Link className="create-import-link" to="/import" onClick={() => onCreated?.()}>or import from docker run / compose →</Link>}
      </div>
      {mode === "wizard" ? <Wizard kind={kind} onCreated={onCreated} /> : <NewUnitForm initialKind={kind} onCreated={onCreated} />}
    </div>
  );
}

function NewUnit() {
  const [params] = useSearchParams();
  return (
    <Page title="New unit" kicker="Create a Quadlet from a starter template" back={<BackButton />}>
      <Panel title="Definition" icon={Plus}>
        <NewUnitForm initialKind={params.get("kind") || undefined} />
      </Panel>
    </Page>
  );
}

function NewUnitForm({ onCreated, initialKind }: { onCreated?: () => void; initialKind?: string }) {
  const api = useApi();
  const { toast } = useApiContext();
  const navigate = useNavigate();
  const startKind = initialKind && TEMPLATES[initialKind] ? initialKind : "container";
  const [scopes, setScopes] = useState(["system"]);
  const [kind, setKind] = useState(startKind);
  const [scope, setScope] = useState("system");
  const [baseName, setBaseName] = useState("");
  const [content, setContent] = useState(TEMPLATES[startKind]);
  const [validation, setValidation] = useState<{ validation?: ValidationResult; hints?: string[] } | null>(null);
  const dirty = baseName.trim() !== "" || content !== TEMPLATES[kind];
  useDirtyGuard(dirty);

  useEffect(() => {
    api<HostInfo>("/api/host").then(({ body }) => {
      const next = body.scopes || ["system"];
      setScopes(next);
      setScope(next[0] || "system");
    }).catch(() => undefined);
  }, [api]);

  function changeKind(next: string) {
    setKind(next);
    setContent(TEMPLATES[next]);
  }

  async function create() {
    if (!baseName.trim()) {
      toast("name required", true);
      return;
    }
    const name = `${baseName.trim()}.${kind}`;
    try {
      const { status, body } = await api<{ validation?: ValidationResult; hints?: string[] }>(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`, { method: "PUT", body: JSON.stringify({ content }) });
      setValidation(body);
      if (status === 422) {
        toast("rejected by validator", true);
        return;
      }
      toast(`created ${name}`);
      onCreated?.();
      navigate(`/unit/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`);
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  return (
    <>
      <div className="filterbar">
        <input className="input" placeholder="name, e.g. nginx-edge" value={baseName} onChange={(e) => setBaseName(e.target.value)} />
        <select className="input" value={kind} onChange={(e) => changeKind(e.target.value)}>{Object.keys(TEMPLATES).map((k) => <option key={k}>{k}</option>)}</select>
        <select className="input" value={scope} onChange={(e) => setScope(e.target.value)}>{scopes.map((s) => <option key={s}>{s}</option>)}</select>
      </div>
      <CodeEditor value={content} onChange={setContent} />
      <button className="btn btn-accent" onClick={create}><Check size={16} /> Validate + create</button>
      {validation && <ValidationBlock validation={validation.validation} hints={validation.hints || []} />}
    </>
  );
}

function ImportView() {
  const api = useApi();
  const { toast } = useApiContext();
  const [scopes, setScopes] = useState(["system"]);
  const [scope, setScope] = useState("system");
  const [kind, setKind] = useState<keyof typeof IMPORT_MODES>("run");
  const [input, setInput] = useState("");
  const [containers, setContainers] = useState<Array<{ id: string; name: string; image: string; state: string; managed?: boolean }>>([]);
  const [results, setResults] = useState<Array<{ name: string; content: string; warnings?: string[] }>>([]);

  useEffect(() => {
    api<HostInfo>("/api/host").then(({ body }) => {
      const next = body.scopes || ["system"];
      setScopes(next);
      setScope(next[0] || "system");
    }).catch(() => undefined);
  }, [api]);

  useEffect(() => {
    if (kind !== "container") return;
    api<{ containers?: typeof containers }>("/api/import/containers").then(({ body }) => setContainers(body.containers || [])).catch((e) => toast((e as Error).message, true));
  }, [api, kind, toast]);

  async function convert() {
    const payload = kind === "container" ? input || containers.find((c) => !c.managed)?.id || "" : input;
    if (!payload) {
      toast("nothing to convert", true);
      return;
    }
    try {
      const { status, body } = await api<{ units?: typeof results; error?: string }>("/api/convert", { method: "POST", body: JSON.stringify({ kind, input: payload }) });
      if (status === 422) {
        toast(body.error || "conversion failed", true);
        return;
      }
      setResults(body.units || []);
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  function selectKind(next: keyof typeof IMPORT_MODES) {
    setKind(next);
    setInput("");
    setResults([]);
  }

  function useSample() {
    if (kind === "container") return;
    setInput(IMPORT_MODES[kind].placeholder);
  }

  const selectedMode = IMPORT_MODES[kind];

  return (
    <Page title="Import" kicker="Convert existing definitions into Quadlets">
      <Panel title="Source" icon={Import}>
        {kind === "container" && <p className="banner">Local host only. Container import uses this Rookery host's Podman socket.</p>}
        <div className="import-layout">
          <div className="import-modes" role="tablist" aria-label="Import source type">
            {Object.entries(IMPORT_MODES).map(([k, m]) => {
              const active = k === kind;
              return (
                <button className={`import-mode-card ${active ? "active" : ""}`} key={k} type="button" role="tab" aria-selected={active} onClick={() => selectKind(k as keyof typeof IMPORT_MODES)}>
                  <span className="import-mode-icon">{k === "container" ? <Boxes size={18} /> : k === "compose" ? <ListFilter size={18} /> : <Import size={18} />}</span>
                  <span>
                    <strong>{m.label}</strong>
                    <small>{m.help}</small>
                  </span>
                </button>
              );
            })}
          </div>
          <div className="source-card">
            <div className="source-card-head">
              <div>
                <p className="kicker">Selected source</p>
                <h2>{selectedMode.label}</h2>
              </div>
              <label className="scope-picker">
                <span>Target scope</span>
                <select className="input" value={scope} onChange={(e) => setScope(e.target.value)}>{scopes.map((s) => <option key={s}>{s}</option>)}</select>
              </label>
            </div>
            <p className="muted">{selectedMode.help}</p>
            {kind === "container" ? (
              containers.length ? <select className="input wide" value={input} onChange={(e) => setInput(e.target.value)}>{containers.map((c) => <option key={c.id} value={c.id} disabled={c.managed}>{c.name} - {c.image} ({c.state}){c.managed ? " - already managed" : ""}</option>)}</select> : <p className="banner banner-warn">No containers found via the Podman API socket.</p>
            ) : (
              <>
                <div className="editor-actions">
                  <button className="btn btn-sm" type="button" onClick={useSample}><SquarePen size={14} /> Use sample</button>
                  {input && <button className="btn btn-sm btn-ghost" type="button" onClick={() => setInput("")}><X size={14} /> Clear</button>}
                </div>
                <textarea className="code-editor" placeholder={selectedMode.placeholder} value={input} onChange={(e) => setInput(e.target.value)} />
              </>
            )}
            <div className="import-convert-row"><button className="btn btn-accent" onClick={convert}><RefreshCw size={16} /> Convert</button></div>
          </div>
        </div>
      </Panel>
      {results.length > 0 && <Panel title={`${results.length} generated unit${results.length === 1 ? "" : "s"}`} icon={Boxes}>{results.map((r, i) => <ImportResult key={i} unit={r} scope={scope} sourceContainer={kind === "container" ? input : ""} />)}</Panel>}
    </Page>
  );
}

function ImportResult({ unit, scope, sourceContainer = "" }: { unit: { name: string; content: string; warnings?: string[] }; scope: string; sourceContainer?: string }) {
  const api = useApi();
  const { toast } = useApiContext();
  const [name, setName] = useState(unit.name);
  const [content, setContent] = useState(unit.content);
  const [status, setStatus] = useState<React.ReactNode>(null);
  const [stopSource, setStopSource] = useState(false);
  const [startManaged, setStartManaged] = useState(true);
  const created = !!status;
  useDirtyGuard(!created && (name !== unit.name || content !== unit.content));

  async function create(adopt = false) {
    try {
      const { status: code, body } = await api<{ validation?: ValidationResult; hints?: string[] }>(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`, { method: "PUT", body: JSON.stringify({ content }) });
      if (code === 422) {
        setStatus(<ValidationBlock validation={body.validation} hints={body.hints || []} />);
        return;
      }
      if (adopt && stopSource && sourceContainer) {
        await api(`/api/import/containers/${encodeURIComponent(sourceContainer)}/stop`, { method: "POST", body: "{}" });
      }
      if (adopt && startManaged) {
        await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/action`, { method: "POST", body: JSON.stringify({ action: "start" }) });
      }
      setStatus(<span>created - <Link to={`/unit/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`}>open {name}</Link></span>);
      toast(adopt ? `adopted ${name}` : `created ${name}`);
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  return (
    <div className="import-result">
      <div className="filterbar"><input className="input" value={name} onChange={(e) => setName(e.target.value)} /><button className="btn btn-accent" onClick={() => create(false)}><Plus size={16} /> Create</button>{sourceContainer && <button className="btn" onClick={() => create(true)}><Check size={16} /> Adopt</button>}{status}</div>
      {sourceContainer && <div className="action-row"><label className="check"><input type="checkbox" checked={stopSource} onChange={(e) => setStopSource(e.target.checked)} /> stop original container</label><label className="check"><input type="checkbox" checked={startManaged} onChange={(e) => setStartManaged(e.target.checked)} /> start managed unit</label></div>}
      {unit.warnings?.map((w) => <p className="banner banner-warn" key={w}>{w}</p>)}
      <CodeEditor value={content} onChange={setContent} small />
    </div>
  );
}

// ImagesView owns /images: the image/build units plus registry-drift checks and
// stale-image cleanup (the former standalone Updates page, folded in — updates
// are "mostly an image thing").
function ImagesView({ view }: { view: ResourceView }) {
  const api = useApi();
  const { toast } = useApiContext();
  const { sel } = useNodeSel();
  const { resources, reload: reloadResources } = useResources(true);
  const storeImages = resources.filter((r) => r.kind === "image" && onNode(sel, r.node)).sort((a, b) => a.name.localeCompare(b.name));
  const [params, setParams] = useSearchParams();
  const [allUpdates, setUpdates] = useState<UpdateInfo[]>([]);
  const [summary, setSummary] = useState("");
  const [stale, setStale] = useState<{ count: number; bytes: number } | null>(null);
  const [operation, setOperation] = useState<{ title: string; lines: string[] } | null>(null);
  const [q, setQ] = useState(params.get("q") || "");
  const updates = allUpdates.filter((u) => onNode(sel, u.node));
  const available = updates.filter((u) => u.updateAvailable);
  const noted = updates.filter((u) => u.note && !u.updateAvailable);
  const current = updates.filter((u) => !u.note && !u.updateAvailable);
  // Stale (dangling) prune only covers the local store.
  const localSelected = onNode(sel, "local");
  const needle = q.trim().toLowerCase();
  const shownImages = storeImages.filter((im) => !needle || `${im.name} ${im.scope}`.toLowerCase().includes(needle));
  useEffect(() => {
    const next = new URLSearchParams();
    if (q) next.set("q", q);
    setParams(next, { replace: true });
  }, [q, setParams]);

  async function refreshStaleImages() {
    const staleResp = await api<{ count: number; bytes: number }>("/api/images/stale").catch(() => null);
    setStale(staleResp?.body || null);
  }

  async function check(showOverlay = true) {
    try {
      if (showOverlay) setOperation({ title: "Checking image updates", lines: ["Scanning managed container units", "Comparing registry digests with host image stores"] });
      const { body } = await api<{ updates?: UpdateInfo[]; skippedScopes?: string[] }>("/api/updates");
      const rows = body.updates || [];
      setUpdates(rows);
      const checked = rows.filter((r) => !r.note).length;
      const count = rows.filter((r) => r.updateAvailable).length;
      const skipped = body.skippedScopes?.length ? `; skipped ${body.skippedScopes.join(", ")}` : "";
      setSummary(count ? `${count} updates available (${checked} tags checked${skipped})` : `all ${checked} checked tags up to date${skipped}`);
      if (showOverlay) setOperation({ title: "Checking image updates", lines: [`Checked ${checked} tags`, "Refreshing stale image totals"] });
      await refreshStaleImages();
    } catch (e) {
      toast((e as Error).message, true);
    } finally {
      if (showOverlay) setOperation(null);
    }
  }

  async function prune() {
    try {
      setOperation({ title: "Pruning stale images", lines: [`Removing ${stale?.count || 0} stale images`, "Podman is reclaiming unused layers"] });
      const { body } = await api<{ reclaimedBytes?: number }>("/api/images/prune", { method: "POST", body: "{}" });
      toast(`pruned; reclaimed ${fmtBytes(body.reclaimedBytes || 0)}`);
      setOperation({ title: "Pruning stale images", lines: [`Reclaimed ${fmtBytes(body.reclaimedBytes || 0)}`, "Refreshing image update state"] });
      await refreshStaleImages();
      await check();
    } catch (e) {
      toast((e as Error).message, true);
    } finally {
      setOperation(null);
    }
  }

  // Prune every image no container references (podman prune --all) across all
  // scopes — or just the selected node's: local stores natively, agent nodes
  // via per-image delete. Confirmed since it removes tagged images.
  async function pruneUnused() {
    if (!confirm(`Remove every image not used by a container, on ${sel ? `node ${sel}` : "every node"}? This cannot be undone.`)) return;
    try {
      setOperation({ title: "Pruning unused images", lines: ["Removing images no container references", "Podman is reclaiming layers"] });
      const { body } = await api<{ reclaimedBytes?: number; removed?: number; scopeErrors?: Record<string, string> }>(`/api/images/prune?all=true${sel ? `&node=${encodeURIComponent(sel)}` : ""}`, { method: "POST", body: "{}" });
      const errs = Object.entries(body.scopeErrors || {});
      toast(`removed ${body.removed || 0} unused images; reclaimed ${fmtBytes(body.reclaimedBytes || 0)}${errs.length ? ` — ${errs.length} scope(s) failed` : ""}`, errs.length > 0);
      await refreshStaleImages();
      await reloadResources();
      await check(false);
    } catch (e) {
      toast((e as Error).message, true);
    } finally {
      setOperation(null);
    }
  }

  async function updateAll() {
    if (!available.length || !confirm(`Pull and restart ${available.length} drifted units${sel ? ` on node ${sel}` : ""}?`)) return;
    try {
      setOperation({ title: "Applying image updates", lines: [`Updating ${available.length} units`, "Pulling images and restarting services"] });
      // With a node selected, send the node's drifted units explicitly instead
      // of allDrifted so other nodes are untouched.
      const payload = sel ? { units: available.map((r) => ({ scope: r.scope, name: r.name })) } : { allDrifted: true };
      const { body } = await api<{ results?: Array<{ ok: boolean; scope: string; name: string; error?: string }> }>("/api/updates/apply", { method: "POST", body: JSON.stringify(payload) });
      const results = body.results || [];
      const failed = results.filter((r) => !r.ok);
      toast(failed.length ? `updates: ${results.length - failed.length} ok — ${failureSummary(failed)}` : `updated ${results.length} units`, failed.length > 0);
      setOperation({ title: "Applying image updates", lines: [`${results.length - failed.length} updated`, ...failed.map((r) => `failed: ${r.name} — ${r.error || "unknown error"}`)] });
      await check(false);
    } catch (e) {
      toast((e as Error).message, true);
    } finally {
      setOperation(null);
    }
  }

  useEffect(() => { check(false); }, []);

  return (
    <Page title={view.label} subtitle="Image units, updates, and cleanup">
      {operation && <OperationOverlay title={operation.title} lines={operation.lines} onClose={() => setOperation(null)} />}
      {localSelected && <p className="banner">Stale-image prune and container import are local-host operations; "Prune unused" and update checks cover every node.</p>}
      <div className="tiles">
        <MetricTile label="updates available" value={available.length} tone={available.length ? "warn" : "dim"} />
        <MetricTile label="current" value={current.length} tone={current.length ? "ok" : "dim"} />
        <MetricTile label="skipped / errors" value={noted.length} tone={noted.length ? "warn" : "dim"} />
        {localSelected && <MetricTile label="stale images" value={stale?.count || 0} tone={stale?.count ? "warn" : "dim"} />}
      </div>
      <div className="action-row"><button className="btn btn-accent" disabled={!!operation} onClick={() => check()}><RefreshCw size={16} /> Check image updates</button>{available.length > 0 && <button className="btn" disabled={!!operation} onClick={updateAll}><Download size={16} /> Update all</button>}{summary && <span className="muted">{summary}</span>}{localSelected && stale?.count ? <button className="btn" disabled={!!operation} onClick={prune}><Trash2 size={16} /> Prune {stale.count} stale ({fmtBytes(stale.bytes)})</button> : null}</div>
      <Panel title="Available updates" icon={Download}>
        {available.length ? available.map((row) => <UpdateRow key={`${row.scope}/${row.name}`} row={row} after={() => check(false)} busy={!!operation} />) : <p className="muted">No image updates currently flagged.</p>}
      </Panel>
      <Panel title={`Images in store (${storeImages.length})`} icon={Package}>
        <div className="filterbar units-filterbar">
          <label className="searchbox"><Search size={16} /><input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter images by name, scope..." /></label>
          <button className="btn" disabled={!!operation} onClick={pruneUnused}><Trash2 size={16} /> Prune unused</button>
        </div>
        {storeImages.length ? shownImages.length ? (
          <div className="unit-list">{shownImages.map((im, i) => <ResourceRow key={`${im.node || ""}/${im.scope}/${im.name}/${i}`} res={im} onChanged={reloadResources} updateAvailable={updates.some((u) => u.updateAvailable && u.image === im.name)} />)}</div>
        ) : <EmptyState title="No matching images" text="Adjust the filter above." /> : <p className="muted">No tagged images in the store yet.</p>}
      </Panel>
    </Page>
  );
}

function UpdateRow({ row, after, busy }: { row: UpdateInfo; after: () => Promise<void> | void; busy?: boolean }) {
  const api = useApi();
  const { toast } = useApiContext();
  const [operation, setOperation] = useState<{ title: string; lines: string[] } | null>(null);
  async function update() {
    try {
      setOperation({ title: `Updating ${row.name}`, lines: [`Pulling ${row.image}`, `Restarting ${row.scope}/${row.name}`] });
      const { body } = await api<{ pulled?: string; warnings?: string[] }>(`/api/units/${encodeURIComponent(row.scope)}/${encodeURIComponent(row.name)}/update`, { method: "POST", body: "{}" });
      toast(`pulled and restarted ${row.name}`);
      const warnings = body.warnings || [];
      setOperation({ title: `Updating ${row.name}`, lines: [`Pulled ${body.pulled || row.image}`, warnings.length ? warnings.join("; ") : "Refreshing update state"] });
      await after();
    } catch (e) {
      toast((e as Error).message, true);
    } finally {
      setOperation(null);
    }
  }
  return (
    <div className="history-row">
      {operation && <OperationOverlay title={operation.title} lines={operation.lines} onClose={() => setOperation(null)} />}
      <span className="grow"><Link to={`/unit/${encodeURIComponent(row.scope)}/${encodeURIComponent(row.name)}`}>{row.name}</Link><span className="muted"> {row.image}</span></span>
      <button className="btn btn-accent" disabled={busy || !!operation} onClick={update}><Download size={16} /> Pull + restart</button>
    </div>
  );
}

// ResourcesView: per-node hardware inventory — pick a node and see its CPU/mem/
// load/cores, host identity, and GPUs.
function ResourcesView() {
  const api = useApi();
  const { sel, setSel, nodes } = useNodeSel();
  const [devices, setDevices] = useState<GPUDevice[]>([]);
  useEffect(() => {
    api<{ devices?: GPUDevice[] }>("/api/gpus").then(({ body }) => setDevices(body.devices || [])).catch(() => undefined);
  }, [api]);
  // This page needs one concrete node; "All nodes" falls back to local. The
  // page dropdown writes through to the global picker so both stay in sync.
  const node = nodes.find((n) => n.id === sel) || nodes.find((n) => n.local) || nodes[0];
  const m = node?.metrics;
  const memPct = m?.memTotalKb ? Math.round(100 * (1 - (m.memAvailKb || 0) / m.memTotalKb)) : null;
  const nodeGpus = devices.filter((d) => gpuOnNode(d, node));
  return (
    <Page title="Resources" subtitle="Host inventory and utilization">
      {nodes.length > 1 && (
        <div className="filterbar resources-bar"><select className="input" value={node?.id || ""} onChange={(e) => setSel(e.target.value)}>{nodes.map((n) => <option key={n.id} value={n.id}>{n.displayName || (n.local ? "local" : n.id)}{n.metrics?.hostname && n.metrics.hostname !== n.id ? ` · ${n.metrics.hostname}` : ""}</option>)}</select></div>
      )}
      <div className="tiles">
        {m?.cpuPct != null && m.cpuPct >= 0 && <MetricTile label="cpu" value={`${m.cpuPct}%`} tone="dim" meter={m.cpuPct} />}
        {memPct != null && <MetricTile label="memory" value={`${memPct}%`} tone="dim" meter={memPct} />}
        {m?.load1 != null && <MetricTile label="load" value={m.load1.toFixed(2)} tone="dim" />}
        {m?.cores != null && <MetricTile label="cores" value={m.cores} tone="dim" />}
      </div>
      <dl className="kv">
        {m?.hostname && <><dt>hostname</dt><dd>{m.hostname}</dd></>}
        {m?.kernel && <><dt>kernel</dt><dd>{m.kernel}</dd></>}
        {m?.memTotalKb ? <><dt>memory</dt><dd>{fmtBytes(m.memTotalKb * 1024)}</dd></> : null}
        {!m && <><dt>metrics</dt><dd className="muted">not reported for this node yet</dd></>}
      </dl>
      {nodeGpus.length > 0 && (
        <Panel title="GPUs" icon={Gpu}>
          {nodeGpus.map((d) => <GpuRow key={`${d.host || "local"}-${d.name}`} device={d} />)}
        </Panel>
      )}
    </Page>
  );
}

function GpuRow({ device }: { device: GPUDevice }) {
  const memPct = device.memoryTotalMb > 0 && device.memoryUsedMb >= 0 ? Math.round((100 * device.memoryUsedMb) / device.memoryTotalMb) : null;
  return (
    <div className="gpu-row">
      <span className="gpu-name"><span className="badge badge-gpu">{device.vendor}</span>{device.host && <span className="badge badge-user">{device.host}</span>}{device.name}</span>
      {device.utilizationPct >= 0 ? <Meter label="util" pct={device.utilizationPct} text={`${device.utilizationPct}%`} /> : <span className="muted">util n/a</span>}
      {memPct != null ? <Meter label="vram" pct={memPct} text={`${device.memoryUsedMb} / ${device.memoryTotalMb} MB`} /> : <span className="muted">vram n/a</span>}
    </div>
  );
}

function SecretsView() {
  const api = useApi();
  const { toast } = useApiContext();
  const [secrets, setSecrets] = useState<Array<{ name: string; driver?: string }>>([]);
  const [usedBy, setUsedBy] = useState<Record<string, string[]>>({});
  const [name, setName] = useState("");
  const [data, setData] = useState("");
  const [q, setQ] = useState("");
  const load = useCallback(async () => {
    const { body } = await api<{ secrets?: typeof secrets; usedBy?: Record<string, string[]> }>("/api/secrets");
    setSecrets(body.secrets || []);
    setUsedBy(body.usedBy || {});
  }, [api]);
  useEffect(() => { load().catch((e) => toast((e as Error).message, true)); }, [load, toast]);
  async function create() {
    if (!name || !data) {
      toast("name and value are required", true);
      return;
    }
    try {
      await api("/api/secrets", { method: "POST", body: JSON.stringify({ name, data }) });
      setName("");
      setData("");
      toast(`created secret ${name}`);
      load();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  async function del(secret: string) {
    if (!confirm(`Delete secret ${secret}?`)) return;
    try {
      await api(`/api/secrets/${encodeURIComponent(secret)}`, { method: "DELETE" });
      toast(`deleted secret ${secret}`);
      load();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  const filteredSecrets = secrets.filter((s) => {
    const needle = q.trim().toLowerCase();
    return !needle || `${s.name} ${s.driver || ""} ${(usedBy[s.name] || []).join(" ")}`.toLowerCase().includes(needle);
  });
  return (
    <Page title="Secrets" kicker="Write-only Podman secrets">
      <Panel title="Stored secrets" icon={KeyRound}>
        <p className="banner">Local host only. Remote Podman secrets are not managed from this page.</p>
        <div className="filterbar"><label className="searchbox"><Search size={16} /><input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search secrets..." /></label></div>
        {filteredSecrets.length ? filteredSecrets.map((s) => <div className="history-row" key={s.name}><code>{s.name}</code><span className="badge">{s.driver || "file"}</span><span className="grow muted">{(usedBy[s.name] || []).join(", ") || "not referenced by any unit"}</span><button className="btn btn-sm btn-danger" onClick={() => del(s.name)}><Trash2 size={14} /> delete</button></div>) : <p className="muted">{secrets.length ? "No matching secrets." : "No secrets yet."}</p>}
      </Panel>
      <Panel title="New secret" icon={Plus}>
        <input className="input wide" placeholder="name, e.g. db-password" value={name} onChange={(e) => setName(e.target.value)} />
        <textarea className="code-editor short" placeholder="secret value" value={data} onChange={(e) => setData(e.target.value)} />
        <button className="btn btn-accent" onClick={create}><Plus size={16} /> Create</button>
      </Panel>
    </Page>
  );
}

type LocalUser = { name: string; email?: string; role: string; mustChangePassword?: boolean; mustSetEmail?: boolean };
type APIToken = { name: string; role: string; expiresAt?: string; lastUsedAt?: string; createdAt?: string };
type SettingItem = { key: string; label: string; value: unknown; source: string; locked: boolean; editable: boolean; restartRequired?: boolean };
type SettingGroup = { name: string; items: SettingItem[] };

function SettingsView({ host }: { host: HostInfo | null }) {
  const { auth } = useApiContext();
  const tabs = auth.readOnly && auth.role !== "admin" ? ["Account"] : ["Account", "Users", "Tokens", "Authentication", "Deployment", "Backup", "Audit", "About"];
  const [tab, setTab] = useState(tabs[0]);
  return (
    <Page title="Settings" kicker="Accounts, authentication, and deployment">
      <div className="tabs">
        {tabs.map((name) => <button key={name} className={`tab ${tab === name ? "active" : ""}`} onClick={() => setTab(name)}>{name}</button>)}
      </div>
      {tab === "Account" && <AccountSettings />}
      {tab === "Users" && <UsersSettings />}
      {tab === "Tokens" && <TokensSettings />}
      {tab === "Backup" && <BackupSettings />}
      {tab === "Audit" && <AuditSettings />}
      {tab !== "Account" && tab !== "Users" && tab !== "Tokens" && tab !== "Backup" && tab !== "Audit" && <AppSettings tab={tab} host={host} />}
    </Page>
  );
}

function AccountSettings() {
  const api = useApi();
  const { auth, toast } = useApiContext();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  async function changePassword(ev: FormEvent) {
    ev.preventDefault();
    if (newPassword !== repeat) {
      toast("new passwords do not match", true);
      return;
    }
    try {
      await api("/api/me/password", { method: "POST", body: JSON.stringify({ currentPassword, newPassword }) });
      setCurrentPassword("");
      setNewPassword("");
      setRepeat("");
      toast("password changed");
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  return (
    <Panel title="Account" icon={UserRound}>
      <InfoGrid rows={[["username", auth.username || "anonymous"], ["role", auth.role || (auth.readOnly ? "share" : "admin")]]} />
      {auth.username ? (
        <form className="stack-form account-password-form" onSubmit={changePassword}>
          <input className="input" type="password" autoComplete="current-password" placeholder="Current password" value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)} />
          <input className="input" type="password" autoComplete="new-password" placeholder="New password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} />
          <input className="input" type="password" autoComplete="new-password" placeholder="Repeat new password" value={repeat} onChange={(e) => setRepeat(e.target.value)} />
          <button className="btn btn-accent"><KeyRound size={16} /> Change password</button>
        </form>
      ) : <p className="muted">Share-link sessions cannot change account settings.</p>}
    </Panel>
  );
}

function TokensSettings() {
  const api = useApi();
  const { toast } = useApiContext();
  const [tokens, setTokens] = useState<APIToken[]>([]);
  const [name, setName] = useState("");
  const [role, setRole] = useState("viewer");
  const [expiresAt, setExpiresAt] = useState("");
  const [created, setCreated] = useState("");
  const load = useCallback(async () => {
    const { body } = await api<{ tokens?: APIToken[] }>("/api/tokens");
    setTokens(body.tokens || []);
  }, [api]);
  useEffect(() => { load().catch((e) => toast((e as Error).message, true)); }, [load, toast]);
  async function create() {
    try {
      const { body } = await api<{ token?: string }>("/api/tokens", { method: "POST", body: JSON.stringify({ name, role, expiresAt: expiresAt || undefined }) });
      setCreated(body.token || "");
      setName("");
      setExpiresAt("");
      toast("token created");
      load();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  async function revoke(tokenName: string) {
    try {
      await api(`/api/tokens/${encodeURIComponent(tokenName)}`, { method: "DELETE" });
      toast("token revoked");
      load();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  return (
    <>
      {created && <p className="banner"><b>New token:</b> <code>{created}</code></p>}
      <Panel title="API tokens" icon={KeyRound}>
        {tokens.length ? tokens.map((t) => <div className="history-row" key={t.name}><code>{t.name}</code><span className="badge">{t.role}</span><span className="muted">last used {t.lastUsedAt || "never"}</span><span className="grow" /><button className="btn btn-sm btn-danger" onClick={() => revoke(t.name)}><Trash2 size={14} /> revoke</button></div>) : <p className="muted">No API tokens yet.</p>}
      </Panel>
      <Panel title="Create token" icon={Plus}>
        <div className="filterbar">
          <input className="input" placeholder="name" value={name} onChange={(e) => setName(e.target.value)} />
          <select className="input" value={role} onChange={(e) => setRole(e.target.value)}><option value="viewer">viewer</option><option value="admin">admin</option></select>
          <input className="input" placeholder="expires RFC3339 optional" value={expiresAt} onChange={(e) => setExpiresAt(e.target.value)} />
          <button className="btn btn-accent" disabled={!name} onClick={create}><Plus size={16} /> Create</button>
        </div>
      </Panel>
    </>
  );
}

function BackupSettings() {
  const api = useApi();
  const { toast } = useApiContext();
  const [file, setFile] = useState<File | null>(null);
  const [changes, setChanges] = useState<Array<{ path: string; scope?: string; name?: string; action: string }>>([]);
  async function preview() {
    if (!file) return;
    try {
      const { body } = await api<{ changes?: typeof changes }>("/api/restore?dryRun=1", { method: "POST", body: file, headers: { "Content-Type": "application/gzip" } });
      setChanges(body.changes || []);
      toast("restore preview loaded");
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  async function restore() {
    if (!file || !changes.length || !confirm(`Restore ${changes.length} items from ${file.name}?`)) return;
    try {
      const { body } = await api<{ changes?: typeof changes }>("/api/restore", { method: "POST", body: file, headers: { "Content-Type": "application/gzip" } });
      setChanges(body.changes || []);
      toast("backup restored");
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  return (
    <Panel title="Backup" icon={Download} action={<a className="btn btn-accent" href="/api/backup"><Download size={16} /> Download</a>}>
      <p className="muted">Export Rookery metadata and managed Quadlet files as a tar.gz archive.</p>
      <div className="filterbar">
        <input className="input" type="file" accept=".gz,.tgz,.tar.gz,application/gzip" onChange={(e) => { setFile(e.target.files?.[0] || null); setChanges([]); }} />
        <button className="btn" disabled={!file} onClick={preview}><Upload size={16} /> Preview restore</button>
        <button className="btn btn-danger" disabled={!file || !changes.length} onClick={restore}><RefreshCw size={16} /> Confirm restore</button>
      </div>
      {changes.length > 0 && <div className="settings-list">{changes.map((c) => <div className="history-row" key={c.path}><span className="grow"><code>{c.path}</code></span><span className={`badge ${c.action === "overwrite" ? "badge-warn" : ""}`}>{c.action}</span></div>)}</div>}
    </Panel>
  );
}

function downloadAuditCSV(events: AuditEvent[]) {
  const esc = (v: string) => `"${v.replace(/"/g, '""')}"`;
  const rows = ["time,actor,action,target,detail"];
  for (const ev of events) {
    rows.push([ev.createdAt || "", ev.actor || "system", ev.action, ev.target || "", ev.detail ? JSON.stringify(ev.detail) : ""].map(esc).join(","));
  }
  const url = URL.createObjectURL(new Blob([rows.join("\n") + "\n"], { type: "text/csv" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = "rookery-audit.csv";
  a.click();
  URL.revokeObjectURL(url);
}

function AuditSettings() {
  const api = useApi();
  const { toast } = useApiContext();
  const [params, setParams] = useSearchParams();
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [q, setQ] = useState(params.get("q") || "");
  const [actor, setActor] = useState(params.get("actor") || "all");
  const [action, setAction] = useState(params.get("action") || "all");
  const [sort, setSort] = useState(params.get("sort") || "time");
  const load = useCallback(async () => {
    const { body } = await api<{ events?: AuditEvent[] }>("/api/audit?limit=100");
    setEvents(body.events || []);
  }, [api]);
  useEffect(() => { load().catch((e) => toast((e as Error).message, true)); }, [load, toast]);
  const actors = ["all", ...Array.from(new Set(events.map((event) => event.actor || "system"))).sort()];
  const actions = ["all", ...Array.from(new Set(events.map((event) => event.action))).sort()];
  const filtered = events.filter((event) => {
    const eventActor = event.actor || "system";
    const needle = q.trim().toLowerCase();
    if (actor !== "all" && eventActor !== actor) return false;
    if (action !== "all" && event.action !== action) return false;
    return !needle || `${event.action} ${eventActor} ${event.target || ""} ${JSON.stringify(event.detail || "")}`.toLowerCase().includes(needle);
  }).sort((a, b) => {
    if (sort === "actor") return (a.actor || "system").localeCompare(b.actor || "system");
    if (sort === "action") return a.action.localeCompare(b.action);
    return (b.createdAt || "").localeCompare(a.createdAt || "");
  });
  useEffect(() => {
    const next = new URLSearchParams();
    if (q) next.set("q", q);
    if (actor !== "all") next.set("actor", actor);
    if (action !== "all") next.set("action", action);
    if (sort !== "time") next.set("sort", sort);
    setParams(next, { replace: true });
  }, [action, actor, q, setParams, sort]);
  const authEvents = events.filter((event) => event.action.startsWith("auth.") || event.action.startsWith("setup.") || event.action.startsWith("onboarding.")).length;
  const unitEvents = events.filter((event) => event.action.startsWith("unit.")).length;
  const adminEvents = events.length - authEvents - unitEvents;
  return (
    <>
      <div className="tiles">
        <MetricTile label="events loaded" value={events.length} tone={events.length ? "ok" : "dim"} />
        <MetricTile label="unit events" value={unitEvents} tone={unitEvents ? "ok" : "dim"} />
        <MetricTile label="auth events" value={authEvents} tone={authEvents ? "ok" : "dim"} />
        <MetricTile label="admin events" value={adminEvents} tone={adminEvents ? "warn" : "dim"} />
      </div>
      <Panel title="Audit log" icon={FileClock} action={<>
        <button className="btn btn-sm" onClick={() => downloadAuditCSV(filtered)}><Download size={14} /> Export CSV</button>
        <button className="btn btn-sm" onClick={load}><RefreshCw size={14} /> Refresh</button>
      </>}>
        <div className="filterbar">
          <label className="searchbox"><Search size={16} /><input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter audit events..." /></label>
          <select className="input" value={actor} onChange={(e) => setActor(e.target.value)}>{actors.map((name) => <option key={name}>{name}</option>)}</select>
          <select className="input" value={action} onChange={(e) => setAction(e.target.value)}>{actions.map((name) => <option key={name}>{name}</option>)}</select>
          <select className="input" value={sort} onChange={(e) => setSort(e.target.value)}><option value="time">sort time</option><option value="actor">sort actor</option><option value="action">sort action</option></select>
        </div>
        {filtered.length ? filtered.map((event) => <div className="history-row audit-row" key={event.id}>
        <div>
          <strong>{event.action}</strong>
          <div className="muted">{event.actor || "system"} · {event.target || "rookery"} · {event.createdAt ? new Date(event.createdAt).toLocaleString() : "unknown time"}</div>
          {event.detail != null && <code>{JSON.stringify(event.detail)}</code>}
        </div>
      </div>) : <EmptyState title="No matching audit events" text="Adjust the audit filters or refresh the log." />}
      </Panel>
    </>
  );
}

function UsersSettings() {
  const api = useApi();
  const { toast } = useApiContext();
  const [users, setUsers] = useState<LocalUser[]>([]);
  const [me, setMe] = useState("");
  const [q, setQ] = useState("");
  const [roleFilter, setRoleFilter] = useState("all");
  const [sort, setSort] = useState("name");
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("viewer");
  const [resetUser, setResetUser] = useState("");
  const [resetPasswordValue, setResetPasswordValue] = useState("");
  const load = useCallback(async () => {
    const { body } = await api<{ users?: LocalUser[]; me?: string }>("/api/users");
    setUsers(body.users || []);
    setMe(body.me || "");
  }, [api]);
  useEffect(() => { load().catch((e) => toast((e as Error).message, true)); }, [load, toast]);
  async function create() {
    try {
      await api("/api/users", { method: "POST", body: JSON.stringify({ username, password, role, email }) });
      setUsername("");
      setEmail("");
      setPassword("");
      toast("user created");
      load();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  async function del(name: string) {
    if (!confirm(`Delete user ${name}?`)) return;
    try {
      await api(`/api/users/${encodeURIComponent(name)}`, { method: "DELETE" });
      toast(`deleted ${name}`);
      load();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  async function updateUser(user: LocalUser, patch: Partial<LocalUser>) {
    try {
      await api(`/api/users/${encodeURIComponent(user.name)}`, { method: "PATCH", body: JSON.stringify({ email: patch.email ?? user.email ?? "", role: patch.role ?? user.role }) });
      toast(`updated ${user.name}`);
      load();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  async function resetPassword(name: string, password: string) {
    try {
      await api(`/api/users/${encodeURIComponent(name)}/password`, { method: "POST", body: JSON.stringify({ password }) });
      setResetUser("");
      setResetPasswordValue("");
      toast(`password updated for ${name}`);
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  const admins = users.filter((u) => u.role === "admin").length;
  const viewers = users.filter((u) => u.role === "viewer").length;
  const pending = users.filter((u) => u.mustChangePassword || u.mustSetEmail).length;
  const filtered = users.filter((u) => {
    const needle = q.trim().toLowerCase();
    if (roleFilter !== "all" && u.role !== roleFilter) return false;
    return !needle || `${u.name} ${u.email || ""} ${u.role}`.toLowerCase().includes(needle);
  }).sort((a, b) => {
    const av = sort === "role" ? a.role : sort === "email" ? a.email || "" : a.name;
    const bv = sort === "role" ? b.role : sort === "email" ? b.email || "" : b.name;
    return av.localeCompare(bv);
  });
  return (
    <>
      <div className="tiles">
        <MetricTile label="local users" value={users.length} tone={users.length ? "ok" : "dim"} />
        <MetricTile label="admins" value={admins} tone={admins ? "ok" : "warn"} />
        <MetricTile label="viewers" value={viewers} tone="dim" />
        <MetricTile label="setup pending" value={pending} tone={pending ? "warn" : "dim"} />
      </div>
      <Panel title="Accounts" icon={Users}>
        <div className="filterbar">
          <label className="searchbox"><Search size={16} /><input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter users..." /></label>
          <select className="input" value={roleFilter} onChange={(e) => setRoleFilter(e.target.value)}><option value="all">all roles</option><option value="admin">admin</option><option value="viewer">viewer</option></select>
          <select className="input" value={sort} onChange={(e) => setSort(e.target.value)}><option value="name">sort name</option><option value="email">sort email</option><option value="role">sort role</option></select>
        </div>
        {filtered.length ? filtered.map((u) => <div className="history-row settings-user-row" key={u.name}>
          <code>{u.name}{u.name === me ? " (you)" : ""}</code>
          <input className="input" type="email" placeholder="email" value={u.email || ""} onChange={(e) => setUsers((rows) => rows.map((row) => row.name === u.name ? { ...row, email: e.target.value } : row))} onBlur={() => updateUser(u, { email: u.email || "" })} />
          <select className="input" value={u.role} onChange={(e) => updateUser(u, { role: e.target.value })}><option value="viewer">viewer</option><option value="admin">admin</option></select>
          <span className={`badge ${u.role === "admin" ? "badge-user" : ""}`}>{u.role}</span>
          {u.mustSetEmail && <span className="badge badge-warn">email required</span>}
          {u.mustChangePassword && <span className="badge badge-warn">password reset</span>}
          <span className="grow" />
          <button className="btn btn-sm" onClick={() => { setResetUser(u.name); setResetPasswordValue(""); }}><KeyRound size={14} /> reset</button>
          <button className="btn btn-sm btn-danger" onClick={() => del(u.name)}><Trash2 size={14} /> delete</button>
        </div>) : <EmptyState title="No matching users" text="Adjust the account filters or add a local user." />}
      </Panel>
      {resetUser && <Overlay title={`Reset ${resetUser}`} onClose={() => setResetUser("")}>
        <div className="stack-form">
          <input className="input" type="password" autoComplete="new-password" placeholder="New password" value={resetPasswordValue} onChange={(e) => setResetPasswordValue(e.target.value)} />
          <button className="btn btn-accent" disabled={!resetPasswordValue} onClick={() => resetPassword(resetUser, resetPasswordValue)}><KeyRound size={16} /> Reset password</button>
        </div>
      </Overlay>}
      <Panel title="Add user" icon={Plus}>
        <div className="filterbar">
          <input className="input" placeholder="username" value={username} onChange={(e) => setUsername(e.target.value)} />
          <input className="input" placeholder="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          <input className="input" placeholder="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          <select className="input" value={role} onChange={(e) => setRole(e.target.value)}><option value="viewer">viewer</option><option value="admin">admin</option></select>
          <button className="btn btn-accent" onClick={create}><Plus size={16} /> Add</button>
        </div>
      </Panel>
    </>
  );
}

function AppSettings({ tab, host }: { tab: string; host: HostInfo | null }) {
  const api = useApi();
  const { toast } = useApiContext();
  const [groups, setGroups] = useState<SettingGroup[]>([]);
  const [license, setLicense] = useState<LicenseStatus | null>(null);
  const [draft, setDraft] = useState<Record<string, unknown>>({});
  const [restart, setRestart] = useState(false);
  const load = useCallback(async () => {
    const { body } = await api<{ groups?: SettingGroup[] }>("/api/settings");
    setGroups(body.groups || []);
    setDraft({});
  }, [api]);
  useEffect(() => { load().catch((e) => toast((e as Error).message, true)); }, [load, toast]);
  useEffect(() => {
    if (tab !== "About") return;
    api<{ license?: LicenseStatus }>("/api/license")
      .then(({ body }) => setLicense(body.license || null))
      .catch((e) => toast((e as Error).message, true));
  }, [api, tab, toast]);
  const group = groups.find((g) => g.name === tab);
  async function save() {
    try {
      const { body } = await api<{ restartRequired?: boolean }>("/api/settings", { method: "PUT", body: JSON.stringify({ settings: draft }) });
      setRestart(!!body.restartRequired);
      toast("settings saved");
      load();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  async function testAlerts() {
    try {
      await api("/api/alerts/test", { method: "POST", body: "{}" });
      toast("test alert sent");
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  if (tab === "About") {
    const limitText = (n?: number) => n === 0 ? "unlimited" : n == null ? "unknown" : String(n);
    const rows: Array<[string, string]> = [
      ["version", String(group?.items.find((i) => i.key === "version")?.value || "dev")],
      ["podman", host?.podman?.version || "unknown"],
      ["SELinux", host?.selinuxEnforcing ? "enforcing" : "not enforcing"],
      ["validator", host?.generatorAvailable ? "available" : "unavailable"],
      ["edition", license?.edition || "unknown"],
      ["managed nodes", license ? `${license.managedNodes}/${license.nodeLimit}` : "unknown"],
      ["nodes remaining", license ? String(license.nodesRemaining) : "unknown"],
      ["nodes over limit", license ? String(license.nodesOverLimit) : "unknown"],
      ["local users", limitText(license?.localUserLimit)],
      ["SSO users", limitText(license?.ssoUserLimit)],
      ["enforcement", license?.enforcement || "unknown"],
    ];
    return <Panel title="About" icon={Activity}><InfoGrid rows={rows} />{license?.message && <p className={`banner ${license.enterpriseAvailable ? "" : "banner-warn"}`}>{license.message}</p>}</Panel>;
  }
  return (
    <Panel title={tab} icon={tab === "Authentication" ? Shield : HardDrive} action={Object.keys(draft).length > 0 ? <button className="btn btn-accent" onClick={save}><Save size={16} /> Save</button> : null}>
      {restart && <p className="banner banner-warn">Restart Rookery to apply saved settings.</p>}
      {tab === "Deployment" && <div className="action-row"><button className="btn" onClick={testAlerts}><Zap size={16} /> Send test alert</button></div>}
      <div className="settings-list">
        {(group?.items || []).map((item) => <SettingControl key={item.key} item={item} value={draft[item.key] ?? item.value} onChange={(value) => setDraft((d) => ({ ...d, [item.key]: value }))} />)}
      </div>
    </Panel>
  );
}

type RemoteEntry = { node: string; scope: string; target: string };

function parseRemoteEntries(value: unknown): RemoteEntry[] {
  const raw = String(value ?? "").trim();
  if (!raw) return [];
  return raw.split(",").map((part) => {
    const [aliasRaw, targetRaw = ""] = part.split("=", 2);
    const alias = aliasRaw.trim();
    const [nodeRaw, scopeRaw = ""] = alias.split(".", 2);
    const grouped = ["root", "rootful", "user", "rootless"].includes(scopeRaw);
    const node = grouped ? nodeRaw : alias;
    const scope = grouped ? scopeRaw : "";
    return { node: node.trim(), scope: scope.trim(), target: targetRaw.trim() };
  }).filter((row) => row.node || row.target);
}

function serializeRemoteEntries(rows: RemoteEntry[]): string {
  return rows.map((row) => {
    const node = row.node.trim();
    const scope = row.scope.trim();
    const target = row.target.trim();
    if (!node || !target) return "";
    const alias = scope ? `${node}.${scope}` : node;
    return `${alias}=${target}`;
  }).filter(Boolean).join(",");
}

function RemoteNodesSetting({ item, value, onChange }: { item: SettingItem; value: unknown; onChange: (value: unknown) => void }) {
  const disabled = item.locked || !item.editable;
  const rows = parseRemoteEntries(value);
  const setRows = (next: RemoteEntry[]) => onChange(serializeRemoteEntries(next));
  const update = (idx: number, patch: Partial<RemoteEntry>) => setRows(rows.map((row, i) => i === idx ? { ...row, ...patch } : row));
  const add = () => setRows([...rows, { node: "", scope: "", target: "" }]);
  const remove = (idx: number) => setRows(rows.filter((_, i) => i !== idx));
  return (
    <div className="remote-setting">
      <div className="remote-setting-head">
        <strong>Remote hosts</strong>
        <button className="btn btn-sm" type="button" disabled={disabled} onClick={add}><Plus size={14} /> Add</button>
      </div>
      {rows.length ? rows.map((row, idx) => (
        <div className="remote-setting-row" key={idx}>
          <input className="input" disabled={disabled} placeholder="node" value={row.node} onChange={(e) => update(idx, { node: e.target.value })} />
          <select className="input" disabled={disabled} value={row.scope} onChange={(e) => update(idx, { scope: e.target.value })}>
            <option value="">single</option>
            <option value="root">rootful</option>
            <option value="rootful">rootful alias</option>
            <option value="user">rootless</option>
            <option value="rootless">rootless alias</option>
          </select>
          <input className="input" disabled={disabled} placeholder="ssh target" value={row.target} onChange={(e) => update(idx, { target: e.target.value })} />
          <button className="btn btn-sm btn-danger" type="button" disabled={disabled} onClick={() => remove(idx)}><Trash2 size={14} /> Remove</button>
        </div>
      )) : <p className="muted">No remote nodes configured.</p>}
      <input className="input remote-setting-raw" disabled={disabled} value={String(value ?? "")} onChange={(e) => onChange(e.target.value)} />
    </div>
  );
}

function SettingControl({ item, value, onChange }: { item: SettingItem; value: unknown; onChange: (value: unknown) => void }) {
  const disabled = item.locked || !item.editable;
  const source = item.locked ? `${item.source} locked` : item.source;
  const boolValue = value === true || value === "true";
  return (
    <div className="setting-row">
      <div><strong>{item.label}</strong><span className="muted">{source}{item.restartRequired ? " · restart" : ""}</span></div>
      {item.key === "remotes" ? (
        <RemoteNodesSetting item={item} value={value} onChange={onChange} />
      ) : typeof item.value === "boolean" ? (
        <label className="switch"><input type="checkbox" checked={boolValue} disabled={disabled} onChange={(e) => onChange(e.target.checked)} /><span /></label>
      ) : (
        <input className="input" disabled={disabled} value={String(value ?? "")} onChange={(e) => onChange(e.target.value)} />
      )}
    </div>
  );
}

function Page({ title, kicker, subtitle, action, back, children }: { title: string; kicker?: string; subtitle?: string; action?: React.ReactNode; back?: React.ReactNode; children: React.ReactNode }) {
  return (
    <>
      <div className="page-head">
        <div className="title-row">{back}<div>{kicker && <p className="kicker">{kicker}</p>}<h1>{title}</h1>{subtitle && <p className="subtitle">{subtitle}</p>}</div></div>
        {action && <div className="page-actions">{action}</div>}
      </div>
      {children}
    </>
  );
}

function BackButton({ className }: { className?: string }) {
  return <Link className={`btn icon-only ${className || ""}`} to="/"><ChevronLeft size={18} /></Link>;
}

function Panel({ title, icon: Icon, action, children }: { title: string; icon: React.ElementType; action?: React.ReactNode; children: React.ReactNode }) {
  return <section className="panel"><div className="panel-head"><h2><Icon size={16} /> {title}</h2>{action}</div>{children}</section>;
}

function Overlay({ title, onClose, children }: { title: string; onClose?: () => void; children: React.ReactNode }) {
  useEffect(() => {
    if (!onClose) return;
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="overlay-backdrop" role="dialog" aria-modal="true" aria-label={title}>
      <div className="overlay-panel">
        <div className="overlay-head"><h2>{title}</h2>{onClose && <button className="btn icon-only" onClick={onClose} aria-label="Close"><X size={16} /></button>}</div>
        {children}
      </div>
    </div>
  );
}

function OperationOverlay({ title, lines, onClose }: { title: string; lines: string[]; onClose?: () => void }) {
  return (
    <Overlay title={title} onClose={onClose}>
      <div className="operation-body">
        <RefreshCw className="spin" size={20} />
        <div>{lines.map((line) => <div key={line}>{line}</div>)}</div>
      </div>
    </Overlay>
  );
}

function MetricTile({ label, value, tone, meter, onClick, active }: { label: string; value: React.ReactNode; tone?: "ok" | "bad" | "warn" | "dim"; meter?: number; onClick?: () => void; active?: boolean }) {
  const cls = `tile ${tone ? `tile-${tone}` : ""} ${onClick ? "tile-action" : ""} ${active ? "active" : ""}`;
  const content = <><div className="tile-value">{value}</div><div className="tile-label">{label}</div>{meter != null && <div className="meter"><span style={{ width: `${Math.max(0, Math.min(100, meter))}%` }} /></div>}</>;
  return onClick ? <button type="button" className={cls} onClick={onClick}>{content}</button> : <div className={cls}>{content}</div>;
}

function Meter({ label, pct, text }: { label: string; pct: number; text: string }) {
  return <span className="meter-block"><span className="meter-head"><span>{label}</span><span>{text}</span></span><span className="meter"><span style={{ width: `${Math.max(0, Math.min(100, pct))}%` }} /></span></span>;
}

function StatusBadge({ state, label }: { state: UnitState; label: string }) {
  return <span className={`badge status ${state}`}><span className={`dot ${state}`} />{label}</span>;
}

function InfoGrid({ rows }: { rows: Array<[string, string]> }) {
  return <dl className="info-grid">{rows.map(([k, v]) => <React.Fragment key={k}><dt>{k}</dt><dd>{v}</dd></React.Fragment>)}</dl>;
}

function ScopeErrors({ errors }: { errors: Record<string, string> }) {
  return <>{Object.entries(errors).map(([scope, error]) => <p key={scope} className="banner banner-warn">scope <b>{scope}</b>: {error}</p>)}</>;
}

function EmptyState({ title, text, icon: Icon = HardDrive, action }: { title: string; text: string; icon?: React.ElementType; action?: React.ReactNode }) {
  return <div className="empty"><Icon size={38} /><h2>{title}</h2><p className="muted">{text}</p>{action && <div className="empty-action">{action}</div>}</div>;
}
