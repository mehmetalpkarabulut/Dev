const healthBadge = document.getElementById("healthBadge");
const runnerBase = document.getElementById("runnerBase");
const activityEl = document.getElementById("activity");
const workspaceListEl = document.getElementById("workspaceList");

function addActivity(entry) {
  const card = document.createElement("div");
  card.className = "activity-card";

  const head = document.createElement("div");
  head.className = "activity-head";

  const left = document.createElement("div");
  left.textContent = `${entry.method} ${entry.endpoint}`;

  const tag = document.createElement("div");
  tag.className = `tag ${entry.status >= 200 && entry.status < 300 ? "ok" : entry.status >= 400 && entry.status < 500 ? "warn" : "err"}`;
  tag.textContent = entry.status;

  head.append(left, tag);

  const meta = document.createElement("div");
  meta.className = "activity-head";
  meta.textContent = entry.timestamp;

  const body = document.createElement("pre");
  body.className = "activity-code";
  body.textContent = entry.bodyText;

  card.append(head, meta, body);
  activityEl.prepend(card);
}

async function api(path, options = {}) {
  const res = await fetch(path, options);
  const contentType = res.headers.get("content-type") || "";
  const body = contentType.includes("application/json") ? await res.json() : await res.text();
  return { status: res.status, body };
}

function stringifyBody(body) {
  if (typeof body === "string") return body;
  return JSON.stringify(body, null, 2);
}

async function handleRequest(method, endpoint, options) {
  const res = await api(endpoint, options);
  addActivity({
    method,
    endpoint,
    status: res.status,
    bodyText: stringifyBody(res.body),
    timestamp: new Date().toLocaleString()
  });
  return res;
}

async function healthCheck() {
  try {
    const res = await api("/healthz");
    healthBadge.textContent = `HEALTH: ${res.status}`;
    healthBadge.style.color = res.status === 200 ? "#37d6a5" : "#ff6d6d";
  } catch (err) {
    healthBadge.textContent = "HEALTH: ERR";
    healthBadge.style.color = "#ff6d6d";
  }
}

function setWorkspaceInputs(workspace) {
  document.getElementById("wsName").value = workspace;
  document.getElementById("appWs").value = workspace;
  document.getElementById("epWorkspace").value = workspace;
}

function setAppInputs(workspace, app) {
  setWorkspaceInputs(workspace);
  document.getElementById("appName").value = app;
  document.getElementById("epApp").value = app;
  document.getElementById("wsScaleApp").value = app;
}

function renderWorkspaceCard(entry, statusBody) {
  const card = document.createElement("div");
  card.className = "workspace-card";

  const header = document.createElement("div");
  header.className = "workspace-head";

  const title = document.createElement("div");
  title.className = "workspace-title";
  title.textContent = entry.workspace || "(unknown)";

  const actions = document.createElement("div");
  actions.className = "workspace-actions";

  const selectBtn = document.createElement("button");
  selectBtn.className = "btn ghost";
  selectBtn.textContent = "Select";
  selectBtn.onclick = () => setWorkspaceInputs(entry.workspace);

  const statusBtn = document.createElement("button");
  statusBtn.className = "btn ghost";
  statusBtn.textContent = "Status";
  statusBtn.onclick = async () => {
    if (!entry.workspace || !entry.workspace.startsWith("ws-")) {
      addActivity({
        method: "GET",
        endpoint: "/workspace/status",
        status: 400,
        bodyText: "workspace must start with ws-",
        timestamp: new Date().toLocaleString()
      });
      return;
    }
    await handleRequest("GET", `/workspace/status?workspace=${encodeURIComponent(entry.workspace)}`);
  };

  actions.append(selectBtn, statusBtn);
  header.append(title, actions);

  const apps = Array.isArray(entry.apps) ? entry.apps : [];
  const appList = document.createElement("div");
  appList.className = "app-list";
  if (apps.length === 0) {
    const empty = document.createElement("div");
    empty.className = "app-empty";
    empty.textContent = "Uygulama listesi bulunamadı.";
    appList.append(empty);
  } else {
    apps.forEach((app) => {
      const chip = document.createElement("button");
      chip.className = "app-chip";
      chip.textContent = app.app || app.name || "app";
      chip.onclick = () => setAppInputs(entry.workspace, app.app || app.name || "app");
      appList.append(chip);
    });
  }

  const raw = document.createElement("pre");
  raw.className = "workspace-raw";
  raw.textContent = stringifyBody(statusBody || entry);

  card.append(header, appList, raw);
  return card;
}

async function refreshWorkspaces() {
  workspaceListEl.innerHTML = "";
  const res = await api("/workspaces");
  if (res.status !== 200) {
    const err = document.createElement("div");
    err.className = "workspace-empty";
    err.textContent = `Workspaces alınamadı (status ${res.status})`;
    workspaceListEl.append(err);
    addActivity({
      method: "GET",
      endpoint: "/workspaces",
      status: res.status,
      bodyText: stringifyBody(res.body),
      timestamp: new Date().toLocaleString()
    });
    return;
  }

  const list = Array.isArray(res.body) ? res.body : res.body?.items || [];
  if (!list.length) {
    const empty = document.createElement("div");
    empty.className = "workspace-empty";
    empty.textContent = "Workspace bulunamadı.";
    workspaceListEl.append(empty);
    return;
  }

  for (const entry of list) {
    if (!entry.workspace) continue;
    let statusBody = null;
    if (entry.workspace.startsWith("ws-")) {
      const statusRes = await api(`/workspace/status?workspace=${encodeURIComponent(entry.workspace)}`);
      statusBody = statusRes.body;
    }
    const card = renderWorkspaceCard(entry, statusBody);
    workspaceListEl.append(card);
  }
}

const sampleGit = {
  app_name: "demoapp",
  workspace: "ws-demo",
  source: {
    type: "git",
    repo_url: "https://github.com/mehmetalpkarabulut/Dev",
    revision: "main"
  },
  image: {
    project: "demoapp",
    tag: "latest",
    registry: "lenovo:8443"
  },
  deploy: { container_port: 3000 }
};

const sampleZip = {
  app_name: "demoapp",
  workspace: "ws-demo",
  source: {
    type: "zip",
    zip_url: "http://zip-server.tekton-pipelines.svc.cluster.local:8080/app.zip"
  },
  image: {
    project: "demoapp",
    tag: "latest",
    registry: "lenovo:8443"
  },
  deploy: { container_port: 8080 }
};

const sampleLocal = {
  app_name: "demoapp",
  workspace: "ws-demo",
  source: {
    type: "local",
    local_path: "/mnt/projects/demoapp"
  },
  image: {
    project: "demoapp",
    tag: "latest",
    registry: "lenovo:8443"
  },
  deploy: { container_port: 3000 }
};

document.getElementById("sampleGit").onclick = () => {
  document.getElementById("runBody").value = JSON.stringify(sampleGit, null, 2);
};

document.getElementById("sampleZip").onclick = () => {
  document.getElementById("runBody").value = JSON.stringify(sampleZip, null, 2);
};

document.getElementById("sampleLocal").onclick = () => {
  document.getElementById("runBody").value = JSON.stringify(sampleLocal, null, 2);
};

runnerBase.textContent = "Same origin";

document.getElementById("healthBtn").onclick = async () => {
  await handleRequest("GET", "/healthz");
  healthCheck();
};

document.getElementById("runBtn").onclick = async () => {
  const body = document.getElementById("runBody").value;
  await handleRequest("POST", "/run", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body
  });
};

document.getElementById("epBtn").onclick = async () => {
  const ws = document.getElementById("epWorkspace").value.trim();
  const app = document.getElementById("epApp").value.trim();
  if (!ws || !app) return;
  await handleRequest("GET", `/endpoint?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`);
};

document.getElementById("wsList").onclick = async () => {
  await handleRequest("GET", "/workspaces");
};

document.getElementById("wsStatus").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  if (!ws) return;
  await handleRequest("GET", `/workspace/status?workspace=${encodeURIComponent(ws)}`);
};

document.getElementById("wsDelete").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  if (!ws) return;
  await handleRequest("POST", `/workspace/delete?workspace=${encodeURIComponent(ws)}`, { method: "POST" });
};

document.getElementById("wsRestart").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  if (!ws) return;
  await handleRequest("POST", `/workspace/restart?workspace=${encodeURIComponent(ws)}`, { method: "POST" });
};

document.getElementById("wsScale").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  const app = document.getElementById("wsScaleApp").value.trim();
  const rep = document.getElementById("wsScaleRep").value.trim();
  if (!ws || !app || !rep) return;
  await handleRequest("POST", `/workspace/scale?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}&replicas=${encodeURIComponent(rep)}`, { method: "POST" });
};

document.getElementById("appStatus").onclick = async () => {
  const ws = document.getElementById("appWs").value.trim();
  const app = document.getElementById("appName").value.trim();
  if (!ws || !app) return;
  await handleRequest("GET", `/app/status?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`);
};

document.getElementById("appRestart").onclick = async () => {
  const ws = document.getElementById("appWs").value.trim();
  const app = document.getElementById("appName").value.trim();
  if (!ws || !app) return;
  await handleRequest("POST", `/app/restart?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`, { method: "POST" });
};

document.getElementById("appDelete").onclick = async () => {
  const ws = document.getElementById("appWs").value.trim();
  const app = document.getElementById("appName").value.trim();
  if (!ws || !app) return;
  await handleRequest("POST", `/app/delete?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`, { method: "POST" });
};

document.getElementById("clearLog").onclick = () => {
  activityEl.innerHTML = "";
};

document.getElementById("wsRefresh").onclick = () => {
  refreshWorkspaces();
};

healthCheck();
refreshWorkspaces();
