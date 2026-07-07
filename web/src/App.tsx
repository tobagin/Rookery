import {
  Activity,
  AlertTriangle,
  Boxes,
  Check,
  ChevronLeft,
  CircleStop,
  Cpu,
  Download,
  Eye,
  FileClock,
  Gauge,
  Github,
  HardDrive,
  Home,
  Import,
  KeyRound,
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
  Settings,
  Shield,
  SquarePen,
  Sun,
  Trash2,
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
  NodeGroup,
  PolicyFinding,
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
            <Route path="/units" element={<UnitsPage />} />
            <Route path="/failed" element={<UnitsPage failedOnly />} />
            <Route path="/fleet" element={<FleetView />} />
            <Route path="/policies" element={<PoliciesView />} />
            <Route path="/unit/:scope/:name" element={<UnitDetail />} />
            <Route path="/new" element={<AdminOnly><NewUnit /></AdminOnly>} />
            <Route path="/import" element={<AdminOnly><ImportView /></AdminOnly>} />
            <Route path="/updates" element={<AdminOnly><UpdatesView /></AdminOnly>} />
            <Route path="/gpus" element={<GPUsView />} />
            <Route path="/secrets" element={<AdminOnly><SecretsView /></AdminOnly>} />
            <Route path="/settings" element={<AdminOnly><SettingsView host={host} /></AdminOnly>} />
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
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => localStorage.getItem("rookery-sidebar") === "collapsed");
  const nav = navItems(auth.readOnly);

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
    <div className={`app-shell ${sidebarCollapsed ? "sidebar-collapsed" : ""}`}>
      <aside className="sidebar">
        <div className="sidebar-brand-row">
          <Link to="/" className="brand" title="Rookery"><BrandMark /><span className="brand-text">Rookery</span></Link>
          <button className="btn icon-only collapse-btn" onClick={() => setSidebarCollapsed((v) => !v)} title={sidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"} aria-label={sidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"}>
            {sidebarCollapsed ? <PanelLeftOpen size={17} /> : <PanelLeftClose size={17} />}
          </button>
        </div>
        <nav className="side-nav">{groupedNavItems(nav).map((group) => (
          <div className="nav-group" key={group.name}>
            <div className="nav-group-label">{group.name}</div>
            {group.items.map((item) => <NavLinkItem key={item.to} item={item} active={isActive(location.pathname, item.to)} />)}
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
            {!auth.readOnly && <Link className="btn btn-accent" to="/new"><Plus size={16} /> New unit</Link>}
            {!auth.readOnly && auth.required && auth.authenticated && <button className="btn btn-ghost" onClick={copyShare}><Github size={16} /> Share</button>}
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
        {nav.slice(0, 5).map((item) => <NavLinkItem key={item.to} item={item} active={isActive(location.pathname, item.to)} compact />)}
      </nav>
    </div>
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

function navItems(readOnly: boolean) {
  const base = [
    { to: "/", label: "Dashboard", icon: Home, group: "Observe" },
    { to: "/units", label: "Units", icon: Boxes, group: "Operate" },
    { to: "/failed", label: "Failed", icon: AlertTriangle, group: "Operate" },
    { to: "/updates", label: "Updates", icon: Download, group: "Operate", admin: true },
    { to: "/gpus", label: "GPUs", icon: Cpu, group: "Operate" },
    { to: "/fleet", label: "Fleet", icon: Network, group: "Govern" },
    { to: "/policies", label: "Policy", icon: Shield, group: "Govern" },
    { to: "/import", label: "Import", icon: Import, group: "Admin", admin: true },
    { to: "/secrets", label: "Secrets", icon: KeyRound, group: "Admin", admin: true },
    { to: "/settings", label: "Settings", icon: Settings, group: "Admin", admin: true },
  ];
  return base.filter((item) => !readOnly || !item.admin);
}

function groupedNavItems(items: Array<{ to: string; label: string; icon: React.ElementType; group?: string }>) {
  const order = ["Observe", "Operate", "Govern", "Admin"];
  return order.map((name) => ({ name, items: items.filter((item) => (item.group || "Operate") === name) })).filter((group) => group.items.length);
}

function NavLinkItem({ item, active, compact, onClick }: { item: { to: string; label: string; icon: React.ElementType }; active: boolean; compact?: boolean; onClick?: () => void }) {
  const Icon = item.icon;
  return (
    <Link onClick={onClick} className={`${compact ? "bottom-link" : "nav-link"} ${active ? "active" : ""}`} to={item.to} title={item.label}>
      <Icon size={compact ? 20 : 17} />
      <span>{item.label}</span>
    </Link>
  );
}

function isActive(pathname: string, to: string) {
  if (to === "/") return pathname === "/";
  return pathname === to || pathname.startsWith(`${to}/`);
}

function BrandMark() {
  return <span className="brand-mark" aria-hidden="true">R</span>;
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

function Dashboard({ host }: { host: HostInfo | null }) {
  const { units, scopeErrors, error, loading, reload } = useUnits(true);
  const [gpus, setGpus] = useState<GPUDevice[]>([]);
  const api = useApi();

  useEffect(() => {
    api<{ devices?: GPUDevice[] }>("/api/gpus").then(({ body }) => setGpus(body.devices || [])).catch(() => setGpus([]));
  }, [api]);

  const model = useMemo(() => summarizeUnits(units), [units]);
  const failed = units.filter((u) => stateClass(u) === "failed");
  const m = host?.metrics || {};
  const memPct = m.memTotalKb ? Math.round(100 * (1 - (m.memAvailKb || 0) / m.memTotalKb)) : null;

  return (
    <Page title="Dashboard" kicker="Host overview">
      <ScopeErrors errors={scopeErrors} />
      {error && <p className="banner banner-error">{error}</p>}
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

function UnitsPage({ failedOnly = false }: { failedOnly?: boolean }) {
  const { units, scopeErrors, error, loading, reload } = useUnits(true);
  const [q, setQ] = useState("");
  const [kind, setKind] = useState("all");
  const [scope, setScope] = useState("all");
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return units.filter((u) => {
      if (failedOnly && stateClass(u) !== "failed") return false;
      if (kind !== "all" && u.kind !== kind) return false;
      if (scope !== "all" && u.scope !== scope) return false;
      return !needle || `${u.name} ${u.description || ""} ${u.image || ""} ${u.pod || ""}`.toLowerCase().includes(needle);
    });
  }, [failedOnly, kind, q, scope, units]);
  const kinds = ["all", ...Array.from(new Set(units.map((u) => u.kind))).sort()];
  const scopes = ["all", ...Array.from(new Set(units.map((u) => u.scope))).sort()];

  return (
    <Page title={failedOnly ? "Failed units" : "Units"} kicker={failedOnly ? "Triage and restart" : "Search, filter, and act"}>
      <ScopeErrors errors={scopeErrors} />
      {error && <p className="banner banner-error">{error}</p>}
      <div className="filterbar">
        <label className="searchbox"><Search size={16} /><input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter by name, image, pod..." /></label>
        <select className="input" value={kind} onChange={(e) => setKind(e.target.value)}>{kinds.map((k) => <option key={k}>{k}</option>)}</select>
        <select className="input" value={scope} onChange={(e) => setScope(e.target.value)}>{scopes.map((s) => <option key={s}>{s}</option>)}</select>
      </div>
      {loading ? <p className="muted">Loading units...</p> : filtered.length ? (
        <div className="unit-list">{filtered.map((u) => <UnitRow key={`${u.scope}/${u.name}`} unit={u} onChanged={reload} />)}</div>
      ) : <EmptyState title="No matching units" text="Adjust the filters or create a new unit." />}
    </Page>
  );
}

function UnitRow({ unit, onChanged, compact = false }: { unit: Unit; onChanged: () => void; compact?: boolean }) {
  const { auth, toast } = useApiContext();
  const api = useApi();
  const cls = stateClass(unit);
  const canStart = cls === "stopped" || cls === "failed";

  async function action(act: string, ev: React.MouseEvent) {
    ev.preventDefault();
    ev.stopPropagation();
    try {
      await api(`/api/units/${encodeURIComponent(unit.scope)}/${encodeURIComponent(unit.name)}/action`, { method: "POST", body: JSON.stringify({ action: act }) });
      toast(`${act} ${unit.name}: ok`);
      onChanged();
    } catch (e) {
      toast(`${act} ${unit.name}: ${(e as Error).message}`, true);
    }
  }

  return (
    <Link to={`/unit/${encodeURIComponent(unit.scope)}/${encodeURIComponent(unit.name)}`} className={`unit-row ${cls} ${compact ? "is-compact" : ""}`}>
      <span className={`dot ${cls}`} />
      <span className="unit-main">
        <span className="unit-title">{unit.name}</span>
        {!compact && <span className="unit-sub">{unit.description || unit.image || unit.path || ""}</span>}
      </span>
      <span className="badges">
        <StatusBadge state={cls} label={stateLabel(unit)} />
        <span className="badge">{unit.kind}</span>
        {unit.scope !== "system" && <span className="badge badge-user">{unit.scope}</span>}
        {!!unit.restarts && <span className="badge badge-warn">restart {unit.restarts}</span>}
        {unit.pod && <span className="badge">pod {unit.pod.replace(/\.pod$/, "")}</span>}
        {!!unit.gpus?.length && <span className="badge badge-gpu">gpu</span>}
      </span>
      {!auth.readOnly && (
        <span className="row-actions">
          {canStart && <button className="btn icon-only" title="Start" onClick={(e) => action("start", e)}><Play size={16} /></button>}
          {(cls === "running" || cls === "pending") && <button className="btn icon-only" title="Stop" onClick={(e) => action("stop", e)}><CircleStop size={16} /></button>}
          <button className="btn icon-only" title="Restart" onClick={(e) => action("restart", e)}><RefreshCw size={16} /></button>
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
  const cls = unit ? stateClass(unit) : "unknown";

  const load = useCallback(async () => {
    try {
      const { body } = await api<{ unit: Unit; content: string }>(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`);
      setUnit(body.unit);
      setContent(body.content);
      setSavedContent(body.content);
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

  async function lifecycle(action: string) {
    if (!unit) return;
    try {
      await api(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/action`, { method: "POST", body: JSON.stringify({ action }) });
      toast(`${action}: ok`);
      load();
    } catch (e) {
      toast(`${action}: ${(e as Error).message}`, true);
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
  return (
    <Page
      title={unit.name}
      kicker={`${unit.kind} · ${unit.scope}`}
      back={<Link className="btn icon-only" to="/units"><ChevronLeft size={18} /></Link>}
      action={!auth.readOnly && <div className="action-row"><button className="btn" onClick={() => lifecycle("start")}><Play size={16} /> Start</button><button className="btn" onClick={() => lifecycle("stop")}><CircleStop size={16} /> Stop</button><button className="btn" onClick={() => lifecycle("restart")}><RefreshCw size={16} /> Restart</button></div>}
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
      {tab === "logs" && <LogsTab scope={scope} name={name} />}
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
          ["Image", unit.image || "n/a"],
          ["Restarts", String(unit.restarts || 0)],
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
      <textarea className="code-editor" spellCheck={false} readOnly={readOnly} value={content} onChange={(e) => setContent(e.target.value)} />
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

function LogsTab({ scope, name }: { scope: string; name: string }) {
  const [follow, setFollow] = useState(true);
  const [lines, setLines] = useState("");
  const ref = useRef<HTMLPreElement | null>(null);

  useEffect(() => {
    setLines("");
    const src = new EventSource(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}/logs?follow=${follow ? "1" : "0"}&lines=200`);
    src.onmessage = (ev) => {
      let line = ev.data;
      try {
        const j = JSON.parse(ev.data);
        const ts = j.__REALTIME_TIMESTAMP ? new Date(j.__REALTIME_TIMESTAMP / 1000).toLocaleTimeString() : "";
        const msg = typeof j.MESSAGE === "string" ? j.MESSAGE : JSON.stringify(j.MESSAGE);
        line = `${ts}  ${msg}`;
      } catch {
        // Show raw line.
      }
      setLines((prev) => `${prev}${line}\n`);
    };
    src.onerror = () => { if (!follow) src.close(); };
    return () => src.close();
  }, [follow, name, scope]);

  useEffect(() => {
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [lines]);

  return (
    <Panel title="Logs" icon={Logs} action={<label className="check"><input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> follow</label>}>
      <pre ref={ref} className="output logs-output">{lines || "connecting..."}</pre>
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
  return (
    <Page title="Fleet" kicker="Managed Podman nodes">
      <div className="tiles">
        <MetricTile label="managed nodes" value={license ? `${license.managedNodes}/${license.nodeLimit}` : nodes.length} tone={license && !license.enterpriseAvailable ? "warn" : "ok"} />
        <MetricTile label="units" value={totalUnits} tone={totalUnits ? "ok" : "dim"} />
        <MetricTile label="failed" value={failed} tone={failed ? "bad" : "dim"} />
        <MetricTile label="edition" value={license?.edition || "unknown"} tone="dim" />
      </div>
      {license?.message && <p className={`banner ${license.enterpriseAvailable ? "" : "banner-warn"}`}>{license.message}</p>}
      <Panel title="Nodes" icon={Network}>
        {loading ? <p className="muted">Loading nodes...</p> : nodes.length ? nodes.map((node) => <NodeRow key={node.id} node={node} editable={auth.role === "admin" && !auth.readOnly} onChanged={load} />) : <p className="muted">No managed nodes configured.</p>}
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
  const scopeText = node.scopes.map((s) => s.system ? s.label : `${s.label} (${s.user || "user"})`).join(", ");
  async function editLabels() {
    const value = prompt(`Labels for ${node.local ? "local" : node.id}`, node.labels?.join(", ") || "");
    if (value == null) return;
    try {
      await api(`/api/nodes/${encodeURIComponent(node.id)}/labels`, { method: "PATCH", body: JSON.stringify({ labels: value.split(",") }) });
      toast("node labels updated");
      onChanged();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  return (
    <div className="history-row">
      <div>
        <strong>{node.local ? "local" : node.id}</strong>
        <div className="muted">{scopeText || "no scopes"}</div>
        {!!node.labels?.length && <div>{node.labels.map((label) => <span className="badge badge-user" key={label}>{label}</span>)}</div>}
        {node.errors?.length ? <div className="warn-text">{node.errors.join("; ")}</div> : null}
      </div>
      <span className="grow" />
      <span className="badge">{node.units} units</span>
      <span className="badge badge-running">{node.running} running</span>
      {node.failed > 0 && <span className="badge badge-failed">{node.failed} failed</span>}
      {node.unknown > 0 && <span className="badge">{node.unknown} unknown</span>}
      {editable && <button className="btn btn-sm" onClick={editLabels}><SquarePen size={14} /> labels</button>}
    </div>
  );
}

function PoliciesView() {
  const api = useApi();
  const { auth, toast } = useApiContext();
  const [findings, setFindings] = useState<PolicyFinding[]>([]);
  const [loading, setLoading] = useState(true);
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
  const active = findings.filter((f) => !f.waived);
  const critical = active.filter((f) => f.severity === "critical").length;
  const warn = active.filter((f) => f.severity === "warn").length;
  const waived = findings.length - active.length;
  return (
    <Page title="Policy" kicker="Fleet checks">
      <div className="tiles">
        <MetricTile label="active findings" value={active.length} tone={active.length ? "warn" : "dim"} />
        <MetricTile label="critical" value={critical} tone={critical ? "bad" : "dim"} />
        <MetricTile label="warnings" value={warn} tone={warn ? "warn" : "dim"} />
        <MetricTile label="waived" value={waived} tone="dim" />
      </div>
      <Panel title="Findings" icon={Shield}>
        {loading ? <p className="muted">Scanning Quadlet files...</p> : findings.length ? findings.map((finding) => <PolicyRow key={finding.key} finding={finding} editable={auth.role === "admin" && !auth.readOnly} onChanged={load} />) : <p className="muted">No policy findings.</p>}
      </Panel>
    </Page>
  );
}

function PolicyRow({ finding, editable, onChanged }: { finding: PolicyFinding; editable?: boolean; onChanged: () => void }) {
  const api = useApi();
  const { toast } = useApiContext();
  const badge = finding.severity === "critical" ? "badge-failed" : finding.severity === "warn" ? "badge-warn" : "";
  async function waive() {
    const reason = prompt("Reason for waiving this finding", finding.waiverReason || "");
    if (reason == null) return;
    try {
      await api("/api/policies/waivers", { method: "POST", body: JSON.stringify({ key: finding.key, reason }) });
      toast("policy finding waived");
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
      {editable && (finding.waived ? <button className="btn btn-sm" onClick={unwaive}><X size={14} /> unwaive</button> : <button className="btn btn-sm" onClick={waive}><Check size={14} /> waive</button>)}
    </div>
  );
}

function NewUnit() {
  const api = useApi();
  const { toast } = useApiContext();
  const navigate = useNavigate();
  const [scopes, setScopes] = useState(["system"]);
  const [kind, setKind] = useState("container");
  const [scope, setScope] = useState("system");
  const [baseName, setBaseName] = useState("");
  const [content, setContent] = useState(TEMPLATES.container);
  const [validation, setValidation] = useState<{ validation?: ValidationResult; hints?: string[] } | null>(null);

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
      navigate(`/unit/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`);
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  return (
    <Page title="New unit" kicker="Create a Quadlet from a starter template" back={<BackButton />}>
      <Panel title="Definition" icon={Plus}>
        <div className="filterbar">
          <input className="input" placeholder="name, e.g. jellyfin" value={baseName} onChange={(e) => setBaseName(e.target.value)} />
          <select className="input" value={kind} onChange={(e) => changeKind(e.target.value)}>{Object.keys(TEMPLATES).map((k) => <option key={k}>{k}</option>)}</select>
          <select className="input" value={scope} onChange={(e) => setScope(e.target.value)}>{scopes.map((s) => <option key={s}>{s}</option>)}</select>
        </div>
        <textarea className="code-editor" value={content} spellCheck={false} onChange={(e) => setContent(e.target.value)} />
        <button className="btn btn-accent" onClick={create}><Check size={16} /> Validate + create</button>
        {validation && <ValidationBlock validation={validation.validation} hints={validation.hints || []} />}
      </Panel>
    </Page>
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

  return (
    <Page title="Import" kicker="Convert existing definitions into Quadlets" back={<BackButton />}>
      <Panel title="Source" icon={Import}>
        <div className="filterbar">
          <select className="input" value={kind} onChange={(e) => { setKind(e.target.value as keyof typeof IMPORT_MODES); setInput(""); setResults([]); }}>
            {Object.entries(IMPORT_MODES).map(([k, m]) => <option value={k} key={k}>{m.label}</option>)}
          </select>
          <select className="input" value={scope} onChange={(e) => setScope(e.target.value)}>{scopes.map((s) => <option key={s}>{s}</option>)}</select>
        </div>
        <p className="muted">{IMPORT_MODES[kind].help}</p>
        {kind === "container" ? (
          containers.length ? <select className="input wide" value={input} onChange={(e) => setInput(e.target.value)}>{containers.map((c) => <option key={c.id} value={c.id} disabled={c.managed}>{c.name} - {c.image} ({c.state}){c.managed ? " - already managed" : ""}</option>)}</select> : <p className="banner banner-warn">No containers found via the Podman API socket.</p>
        ) : <textarea className="code-editor" placeholder={IMPORT_MODES[kind].placeholder} value={input} onChange={(e) => setInput(e.target.value)} />}
        <button className="btn btn-accent" onClick={convert}><RefreshCw size={16} /> Convert</button>
      </Panel>
      {results.length > 0 && <Panel title={`${results.length} generated unit${results.length === 1 ? "" : "s"}`} icon={Boxes}>{results.map((r, i) => <ImportResult key={i} unit={r} scope={scope} />)}</Panel>}
    </Page>
  );
}

function ImportResult({ unit, scope }: { unit: { name: string; content: string; warnings?: string[] }; scope: string }) {
  const api = useApi();
  const { toast } = useApiContext();
  const [name, setName] = useState(unit.name);
  const [content, setContent] = useState(unit.content);
  const [status, setStatus] = useState<React.ReactNode>(null);

  async function create() {
    try {
      const { status: code, body } = await api<{ validation?: ValidationResult; hints?: string[] }>(`/api/units/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`, { method: "PUT", body: JSON.stringify({ content }) });
      if (code === 422) {
        setStatus(<ValidationBlock validation={body.validation} hints={body.hints || []} />);
        return;
      }
      setStatus(<span>created - <Link to={`/unit/${encodeURIComponent(scope)}/${encodeURIComponent(name)}`}>open {name}</Link></span>);
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  return (
    <div className="import-result">
      <div className="filterbar"><input className="input" value={name} onChange={(e) => setName(e.target.value)} /><button className="btn btn-accent" onClick={create}><Plus size={16} /> Create</button>{status}</div>
      {unit.warnings?.map((w) => <p className="banner banner-warn" key={w}>{w}</p>)}
      <textarea className="code-editor small" value={content} onChange={(e) => setContent(e.target.value)} />
    </div>
  );
}

function UpdatesView() {
  const api = useApi();
  const { toast } = useApiContext();
  const [updates, setUpdates] = useState<UpdateInfo[]>([]);
  const [summary, setSummary] = useState("");
  const [stale, setStale] = useState<{ count: number; bytes: number } | null>(null);
  const available = updates.filter((u) => u.updateAvailable);

  async function check() {
    try {
      const { body } = await api<{ updates?: UpdateInfo[]; skippedScopes?: string[] }>("/api/updates");
      const rows = body.updates || [];
      setUpdates(rows);
      const checked = rows.filter((r) => !r.note).length;
      const count = rows.filter((r) => r.updateAvailable).length;
      setSummary(count ? `${count} updates available (${checked} tags checked)` : `all ${checked} checked tags up to date`);
      const staleResp = await api<{ count: number; bytes: number }>("/api/images/stale").catch(() => null);
      setStale(staleResp?.body || null);
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  async function prune() {
    try {
      const { body } = await api<{ reclaimedBytes?: number }>("/api/images/prune", { method: "POST", body: "{}" });
      toast(`pruned; reclaimed ${fmtBytes(body.reclaimedBytes || 0)}`);
      check();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }

  useEffect(() => { check(); }, []);

  return (
    <Page title="Updates" kicker="Registry drift and stale image cleanup">
      <div className="action-row"><button className="btn btn-accent" onClick={check}><RefreshCw size={16} /> Check image updates</button>{summary && <span className="muted">{summary}</span>}{stale?.count ? <button className="btn" onClick={prune}><Trash2 size={16} /> Prune {stale.count} stale ({fmtBytes(stale.bytes)})</button> : null}</div>
      <Panel title="Available updates" icon={Download}>
        {available.length ? available.map((row) => <UpdateRow key={`${row.scope}/${row.name}`} row={row} after={check} />) : <p className="muted">No image updates currently flagged.</p>}
      </Panel>
      <Panel title="Checked units" icon={ListFilter}>
        {updates.length ? updates.map((row) => <div className="history-row" key={`${row.scope}/${row.name}`}><span className="grow">{row.name}</span><span className="badge">{row.scope}</span><span className={row.updateAvailable ? "warn-text" : "muted"}>{row.updateAvailable ? "update available" : row.note || "current"}</span></div>) : <p className="muted">Run a check to populate results.</p>}
      </Panel>
    </Page>
  );
}

function UpdateRow({ row, after }: { row: UpdateInfo; after: () => void }) {
  const api = useApi();
  const { toast } = useApiContext();
  async function update() {
    try {
      await api(`/api/units/${encodeURIComponent(row.scope)}/${encodeURIComponent(row.name)}/update`, { method: "POST", body: "{}" });
      toast(`pulled and restarted ${row.name}`);
      after();
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  return <div className="history-row"><span className="grow"><Link to={`/unit/${encodeURIComponent(row.scope)}/${encodeURIComponent(row.name)}`}>{row.name}</Link><span className="muted"> {row.image}</span></span><button className="btn btn-accent" onClick={update}><Download size={16} /> Pull + restart</button></div>;
}

function GPUsView() {
  const api = useApi();
  const [devices, setDevices] = useState<GPUDevice[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    api<{ devices?: GPUDevice[] }>("/api/gpus").then(({ body }) => setDevices(body.devices || [])).catch((e) => setError((e as Error).message));
  }, [api]);
  return (
    <Page title="GPUs" kicker="Inventory and utilization">
      {error && <p className="banner banner-error">{error}</p>}
      <Panel title="Devices" icon={Cpu}>
        {devices.length ? devices.map((d) => <GpuRow key={`${d.host || "local"}-${d.name}`} device={d} />) : <p className="muted">No GPU devices detected.</p>}
      </Panel>
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
  return (
    <Page title="Secrets" kicker="Write-only Podman secrets">
      <Panel title="Stored secrets" icon={KeyRound}>
        {secrets.length ? secrets.map((s) => <div className="history-row" key={s.name}><code>{s.name}</code><span className="badge">{s.driver || "file"}</span><span className="grow muted">{(usedBy[s.name] || []).join(", ") || "not referenced by any unit"}</span><button className="btn btn-sm btn-danger" onClick={() => del(s.name)}><Trash2 size={14} /> delete</button></div>) : <p className="muted">No secrets yet.</p>}
      </Panel>
      <Panel title="New secret" icon={Plus}>
        <input className="input wide" placeholder="name, e.g. db-password" value={name} onChange={(e) => setName(e.target.value)} />
        <textarea className="code-editor short" placeholder="secret value" value={data} onChange={(e) => setData(e.target.value)} />
        <button className="btn btn-accent" onClick={create}><Plus size={16} /> Create</button>
      </Panel>
    </Page>
  );
}

type LocalUser = { name: string; email?: string; role: string };
type SettingItem = { key: string; label: string; value: unknown; source: string; locked: boolean; editable: boolean; restartRequired?: boolean };
type SettingGroup = { name: string; items: SettingItem[] };

function SettingsView({ host }: { host: HostInfo | null }) {
  const [tab, setTab] = useState("Users");
  return (
    <Page title="Settings" kicker="Accounts, authentication, and deployment">
      <div className="tabs">
        {["Users", "Authentication", "Deployment", "Backup", "Audit", "About"].map((name) => <button key={name} className={`tab ${tab === name ? "active" : ""}`} onClick={() => setTab(name)}>{name}</button>)}
      </div>
      {tab === "Users" && <UsersSettings />}
      {tab === "Backup" && <BackupSettings />}
      {tab === "Audit" && <AuditSettings />}
      {tab !== "Users" && tab !== "Backup" && tab !== "Audit" && <AppSettings tab={tab} host={host} />}
    </Page>
  );
}

function BackupSettings() {
  return (
    <Panel title="Backup" icon={Download} action={<a className="btn btn-accent" href="/api/backup"><Download size={16} /> Download</a>}>
      <p className="muted">Export Rookery metadata and managed Quadlet files as a tar.gz archive.</p>
    </Panel>
  );
}

function AuditSettings() {
  const api = useApi();
  const { toast } = useApiContext();
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const load = useCallback(async () => {
    const { body } = await api<{ events?: AuditEvent[] }>("/api/audit?limit=100");
    setEvents(body.events || []);
  }, [api]);
  useEffect(() => { load().catch((e) => toast((e as Error).message, true)); }, [load, toast]);
  return (
    <Panel title="Audit log" icon={FileClock}>
      {events.length ? events.map((event) => <div className="history-row" key={event.id}>
        <div>
          <strong>{event.action}</strong>
          <div className="muted">{event.actor || "system"} · {event.target || "rookery"} · {event.createdAt ? new Date(event.createdAt).toLocaleString() : "unknown time"}</div>
          {event.detail != null && <code>{JSON.stringify(event.detail)}</code>}
        </div>
      </div>) : <p className="muted">No audit events yet.</p>}
    </Panel>
  );
}

function UsersSettings() {
  const api = useApi();
  const { toast } = useApiContext();
  const [users, setUsers] = useState<LocalUser[]>([]);
  const [me, setMe] = useState("");
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("viewer");
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
  async function resetPassword(name: string) {
    const password = prompt(`New password for ${name}`);
    if (!password) return;
    try {
      await api(`/api/users/${encodeURIComponent(name)}/password`, { method: "POST", body: JSON.stringify({ password }) });
      toast(`password updated for ${name}`);
    } catch (e) {
      toast((e as Error).message, true);
    }
  }
  return (
    <>
      <Panel title="Accounts" icon={Users}>
        {users.map((u) => <div className="history-row settings-user-row" key={u.name}>
          <code>{u.name}{u.name === me ? " (you)" : ""}</code>
          <input className="input" type="email" placeholder="email" value={u.email || ""} onChange={(e) => setUsers((rows) => rows.map((row) => row.name === u.name ? { ...row, email: e.target.value } : row))} onBlur={() => updateUser(u, { email: u.email || "" })} />
          <select className="input" value={u.role} onChange={(e) => updateUser(u, { role: e.target.value })}><option value="viewer">viewer</option><option value="admin">admin</option></select>
          <span className={`badge ${u.role === "admin" ? "badge-user" : ""}`}>{u.role}</span>
          <span className="grow" />
          <button className="btn btn-sm" onClick={() => resetPassword(u.name)}><KeyRound size={14} /> reset</button>
          <button className="btn btn-sm btn-danger" onClick={() => del(u.name)}><Trash2 size={14} /> delete</button>
        </div>)}
      </Panel>
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
  if (tab === "About") {
    const rows: Array<[string, string]> = [
      ["version", String(group?.items.find((i) => i.key === "version")?.value || "dev")],
      ["podman", host?.podman?.version || "unknown"],
      ["SELinux", host?.selinuxEnforcing ? "enforcing" : "not enforcing"],
      ["validator", host?.generatorAvailable ? "available" : "unavailable"],
      ["edition", license?.edition || "unknown"],
      ["managed nodes", license ? `${license.managedNodes}/${license.nodeLimit}` : "unknown"],
      ["enforcement", license?.enforcement || "unknown"],
    ];
    return <Panel title="About" icon={Activity}><InfoGrid rows={rows} />{license?.message && <p className={`banner ${license.enterpriseAvailable ? "" : "banner-warn"}`}>{license.message}</p>}</Panel>;
  }
  return (
    <Panel title={tab} icon={tab === "Authentication" ? Shield : HardDrive} action={Object.keys(draft).length > 0 ? <button className="btn btn-accent" onClick={save}><Save size={16} /> Save</button> : null}>
      {restart && <p className="banner banner-warn">Restart Rookery to apply saved settings.</p>}
      <div className="settings-list">
        {(group?.items || []).map((item) => <SettingControl key={item.key} item={item} value={draft[item.key] ?? item.value} onChange={(value) => setDraft((d) => ({ ...d, [item.key]: value }))} />)}
      </div>
    </Panel>
  );
}

function SettingControl({ item, value, onChange }: { item: SettingItem; value: unknown; onChange: (value: unknown) => void }) {
  const disabled = item.locked || !item.editable;
  const source = item.locked ? `${item.source} locked` : item.source;
  const boolValue = value === true || value === "true";
  return (
    <div className="setting-row">
      <div><strong>{item.label}</strong><span className="muted">{source}{item.restartRequired ? " · restart" : ""}</span></div>
      {typeof item.value === "boolean" ? (
        <label className="switch"><input type="checkbox" checked={boolValue} disabled={disabled} onChange={(e) => onChange(e.target.checked)} /><span /></label>
      ) : (
        <input className="input" disabled={disabled} value={String(value ?? "")} onChange={(e) => onChange(e.target.value)} />
      )}
    </div>
  );
}

function Page({ title, kicker, action, back, children }: { title: string; kicker?: string; action?: React.ReactNode; back?: React.ReactNode; children: React.ReactNode }) {
  return (
    <>
      <div className="page-head">
        <div className="title-row">{back}<div><p className="kicker">{kicker}</p><h1>{title}</h1></div></div>
        {action && <div className="page-actions">{action}</div>}
      </div>
      {children}
    </>
  );
}

function BackButton() {
  return <Link className="btn icon-only" to="/"><ChevronLeft size={18} /></Link>;
}

function Panel({ title, icon: Icon, action, children }: { title: string; icon: React.ElementType; action?: React.ReactNode; children: React.ReactNode }) {
  return <section className="panel"><div className="panel-head"><h2><Icon size={16} /> {title}</h2>{action}</div>{children}</section>;
}

function MetricTile({ label, value, tone, meter }: { label: string; value: React.ReactNode; tone?: "ok" | "bad" | "warn" | "dim"; meter?: number }) {
  return <div className={`tile ${tone ? `tile-${tone}` : ""}`}><div className="tile-value">{value}</div><div className="tile-label">{label}</div>{meter != null && <div className="meter"><span style={{ width: `${Math.max(0, Math.min(100, meter))}%` }} /></div>}</div>;
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

function EmptyState({ title, text }: { title: string; text: string }) {
  return <div className="empty"><HardDrive size={38} /><h2>{title}</h2><p className="muted">{text}</p></div>;
}
