const healthBadge = document.getElementById("healthBadge");
const activityEl = document.getElementById("activity");
const workspaceListEl = document.getElementById("workspaceList");
const workspaceDetailEl = document.getElementById("workspaceDetail");

let currentWorkspace = null;
let currentApp = null;
let workspacesCache = [];
let activityCache = [];
let wsStatusCache = {};

function addActivity(entry) {
  activityCache.unshift(entry);
  activityCache = activityCache.slice(0, 20);
  renderActivity();
}

function renderActivity() {
  activityEl.innerHTML = "";
  activityCache.forEach((entry) => {
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

    const msg = document.createElement("div");
    msg.className = "activity-msg";
    msg.textContent = entry.message;

    card.append(head, msg);
    activityEl.append(card);
  });
}

async function api(path, options = {}) {
  const res = await fetch(path, options);
  const contentType = res.headers.get("content-type") || "";
  const body = contentType.includes("application/json") ? await res.json() : await res.text();
  return { status: res.status, body };
}

function toMessage(body) {
  if (typeof body === "string") return body;
  if (!body) return "";
  if (body.message) return body.message;
  if (body.status) return body.status;
  return "OK";
}

async function handleRequest(method, endpoint, options) {
  const res = await api(endpoint, options);
  addActivity({
    method,
    endpoint,
    status: res.status,
    message: toMessage(res.body)
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

function setCurrentWorkspace(ws) {
  currentWorkspace = ws;
  currentApp = null;
  renderWorkspaceDetail();
}

function setCurrentApp(app) {
  currentApp = app;
  renderWorkspaceDetail();
}

function renderWorkspaceCard(entry) {
  const card = document.createElement("div");
  card.className = "workspace-card";

  const header = document.createElement("div");
  header.className = "workspace-head";

  const title = document.createElement("div");
  title.className = "workspace-title";
  title.textContent = entry.workspace || "(unknown)";

  const actions = document.createElement("div");
  actions.className = "workspace-actions";

  const openBtn = document.createElement("button");
  openBtn.className = "btn ghost";
  openBtn.textContent = "Open";
  openBtn.onclick = () => setCurrentWorkspace(entry.workspace);

  actions.append(openBtn);
  header.append(title, actions);

  const apps = Array.isArray(entry.apps) ? entry.apps : [];
  const appList = document.createElement("div");
  appList.className = "app-list";
  if (apps.length === 0) {
    const empty = document.createElement("div");
    empty.className = "app-empty";
    empty.textContent = "Uygulama bulunamadi";
    appList.append(empty);
  } else {
    apps.forEach((app) => {
      const chip = document.createElement("button");
      chip.className = "app-chip";
      chip.textContent = app.app || app.name || "app";
      chip.onclick = () => {
        setCurrentWorkspace(entry.workspace);
        setCurrentApp(app.app || app.name || "app");
      };
      appList.append(chip);
    });
  }

  card.append(header, appList);
  return card;
}

async function refreshWorkspaces() {
  workspaceListEl.innerHTML = "";
  const res = await api("/workspaces");
  if (res.status !== 200) {
    const err = document.createElement("div");
    err.className = "workspace-empty";
    err.textContent = `Workspaces alinmadi (status ${res.status})`;
    workspaceListEl.append(err);
    addActivity({ method: "GET", endpoint: "/workspaces", status: res.status, message: toMessage(res.body) });
    return;
  }

  workspacesCache = Array.isArray(res.body) ? res.body : res.body?.items || [];
  if (!workspacesCache.length) {
    const empty = document.createElement("div");
    empty.className = "workspace-empty";
    empty.textContent = "Workspace bulunamadi.";
    workspaceListEl.append(empty);
    return;
  }

  workspacesCache.forEach((entry) => {
    workspaceListEl.append(renderWorkspaceCard(entry));
  });

  await refreshWorkspaceStatus(currentWorkspace);
}

async function refreshWorkspaceStatus(ws) {
  if (!ws || !ws.startsWith("ws-")) return;
  const res = await api(`/workspace/status?workspace=${encodeURIComponent(ws)}`);
  if (res.status === 200) {
    wsStatusCache[ws] = res.body;
    renderWorkspaceDetail();
  }
}

function getPodCounts(status, appName) {
  const pods = Array.isArray(status?.pods) ? status.pods : [];
  const appPods = pods.filter((p) => p.name && p.name.startsWith(`${appName}-`));
  const running = appPods.filter((p) => p.phase === "Running").length;
  return { total: appPods.length, running };
}

function getServicePort(status, appName) {
  const services = Array.isArray(status?.services) ? status.services : [];
  const svc = services.find((s) => s.name === appName);
  return svc?.nodePort || null;
}

function renderWorkspaceDetail() {
  workspaceDetailEl.innerHTML = "";
  if (!currentWorkspace) {
    const empty = document.createElement("div");
    empty.className = "empty-state";
    empty.textContent = "Bir workspace secin.";
    workspaceDetailEl.append(empty);
    return;
  }

  const entry = workspacesCache.find((w) => w.workspace === currentWorkspace) || { workspace: currentWorkspace, apps: [] };
  const apps = Array.isArray(entry.apps) ? entry.apps : [];
  const status = wsStatusCache[currentWorkspace] || {};

  const info = document.createElement("div");
  info.className = "detail-grid";

  const nameCard = document.createElement("div");
  nameCard.className = "detail-card";
  nameCard.innerHTML = `<div class="detail-label">Workspace</div><div class="detail-value">${currentWorkspace}</div>`;

  const appCount = document.createElement("div");
  appCount.className = "detail-card";
  appCount.innerHTML = `<div class="detail-label">Apps</div><div class="detail-value">${apps.length}</div>`;

  info.append(nameCard, appCount);

  const actions = document.createElement("div");
  actions.className = "detail-actions";

  const statusBtn = document.createElement("button");
  statusBtn.className = "btn ghost";
  statusBtn.textContent = "Refresh Status";
  statusBtn.onclick = async () => {
    await refreshWorkspaceStatus(currentWorkspace);
  };

  const restartBtn = document.createElement("button");
  restartBtn.className = "btn ghost";
  restartBtn.textContent = "Restart Workspace";
  restartBtn.onclick = async () => {
    await handleRequest("POST", `/workspace/restart?workspace=${encodeURIComponent(currentWorkspace)}`, { method: "POST" });
  };

  const deleteBtn = document.createElement("button");
  deleteBtn.className = "btn danger";
  deleteBtn.textContent = "Delete Workspace";
  deleteBtn.onclick = async () => {
    await handleRequest("POST", `/workspace/delete?workspace=${encodeURIComponent(currentWorkspace)}`, { method: "POST" });
    await refreshWorkspaces();
    currentWorkspace = null;
    renderWorkspaceDetail();
  };

  actions.append(statusBtn, restartBtn, deleteBtn);

  const appSection = document.createElement("div");
  appSection.className = "detail-grid";

  apps.forEach((app) => {
    const name = app.app || app.name || "app";
    const counts = getPodCounts(status, name);
    const nodePort = getServicePort(status, name);

    const card = document.createElement("div");
    card.className = "detail-card";
    card.innerHTML = `<div class="detail-label">App</div><div class="detail-value">${name}</div>
      <div class="detail-label">Pods</div><div class="detail-value">${counts.running}/${counts.total}</div>
      <div class="detail-label">NodePort</div><div class="detail-value">${nodePort || "-"}</div>`;

    const btns = document.createElement("div");
    btns.className = "detail-actions";

    const endpointBtn = document.createElement("button");
    endpointBtn.className = "btn ghost";
    endpointBtn.textContent = "Endpoint";
    endpointBtn.onclick = async () => {
      const res = await handleRequest("GET", `/endpoint?workspace=${encodeURIComponent(currentWorkspace)}&app=${encodeURIComponent(name)}`);
      if (res.status === 200 && res.body?.endpoint) {
        const link = document.createElement("div");
        link.className = "detail-value";
        link.textContent = res.body.endpoint;
        card.append(link);
      }
    };

    const statusAppBtn = document.createElement("button");
    statusAppBtn.className = "btn ghost";
    statusAppBtn.textContent = "Status";
    statusAppBtn.onclick = async () => {
      await handleRequest("GET", `/app/status?workspace=${encodeURIComponent(currentWorkspace)}&app=${encodeURIComponent(name)}`);
    };

    const restartAppBtn = document.createElement("button");
    restartAppBtn.className = "btn ghost";
    restartAppBtn.textContent = "Restart";
    restartAppBtn.onclick = async () => {
      await handleRequest("POST", `/app/restart?workspace=${encodeURIComponent(currentWorkspace)}&app=${encodeURIComponent(name)}`, { method: "POST" });
    };

    const deleteAppBtn = document.createElement("button");
    deleteAppBtn.className = "btn danger";
    deleteAppBtn.textContent = "Delete";
    deleteAppBtn.onclick = async () => {
      await handleRequest("POST", `/app/delete?workspace=${encodeURIComponent(currentWorkspace)}&app=${encodeURIComponent(name)}`, { method: "POST" });
      await refreshWorkspaces();
      renderWorkspaceDetail();
    };

    const scaleWrap = document.createElement("div");
    scaleWrap.className = "detail-actions";
    const scaleInput = document.createElement("input");
    scaleInput.type = "number";
    scaleInput.min = "1";
    scaleInput.value = counts.total ? counts.total : "1";
    scaleInput.className = "scale-input";
    const scaleBtn = document.createElement("button");
    scaleBtn.className = "btn ghost";
    scaleBtn.textContent = "Scale";
    scaleBtn.onclick = async () => {
      await handleRequest("POST", `/workspace/scale?workspace=${encodeURIComponent(currentWorkspace)}&app=${encodeURIComponent(name)}&replicas=${encodeURIComponent(scaleInput.value)}`, { method: "POST" });
      await refreshWorkspaceStatus(currentWorkspace);
    };
    scaleWrap.append(scaleInput, scaleBtn);

    btns.append(endpointBtn, statusAppBtn, restartAppBtn, deleteAppBtn);

    card.append(btns, scaleWrap);
    appSection.append(card);
  });

  workspaceDetailEl.append(info, actions, appSection);
}

function buildGitPayload() {
  const appName = document.getElementById("gitAppName").value.trim();
  const workspace = document.getElementById("gitWorkspace").value.trim();
  const repoUrl = document.getElementById("gitRepoUrl").value.trim();
  const revision = document.getElementById("gitRevision").value.trim();
  const gitUser = document.getElementById("gitUser").value.trim();
  const gitToken = document.getElementById("gitToken").value.trim();
  const project = document.getElementById("gitImageProject").value.trim();
  const tag = document.getElementById("gitImageTag").value.trim();
  const registry = document.getElementById("gitRegistry").value.trim();
  const port = parseInt(document.getElementById("gitPort").value, 10) || 3000;

  const source = { type: "git", repo_url: repoUrl, revision: revision || "main" };
  if (gitUser) source.git_username = gitUser;
  if (gitToken) source.git_token = gitToken;

  return {
    app_name: appName,
    workspace: workspace,
    source,
    image: {
      project: project || appName,
      tag: tag || "latest",
      registry: registry || "lenovo:8443"
    },
    deploy: { container_port: port }
  };
}

function buildZipPayload() {
  const appName = document.getElementById("zipAppName").value.trim();
  const workspace = document.getElementById("zipWorkspace").value.trim();
  const zipUrl = document.getElementById("zipUrl").value.trim();
  const project = document.getElementById("zipImageProject").value.trim();
  const tag = document.getElementById("zipImageTag").value.trim();
  const registry = document.getElementById("zipRegistry").value.trim();
  const port = parseInt(document.getElementById("zipPort").value, 10) || 8080;

  return {
    app_name: appName,
    workspace: workspace,
    source: { type: "zip", zip_url: zipUrl },
    image: {
      project: project || appName,
      tag: tag || "latest",
      registry: registry || "lenovo:8443"
    },
    deploy: { container_port: port }
  };
}

function buildLocalPayload() {
  const appName = document.getElementById("localAppName").value.trim();
  const workspace = document.getElementById("localWorkspace").value.trim();
  const localPath = document.getElementById("localPath").value.trim();
  const project = document.getElementById("localImageProject").value.trim();
  const tag = document.getElementById("localImageTag").value.trim();
  const registry = document.getElementById("localRegistry").value.trim();
  const port = parseInt(document.getElementById("localPort").value, 10) || 3000;

  return {
    app_name: appName,
    workspace: workspace,
    source: { type: "local", local_path: localPath },
    image: {
      project: project || appName,
      tag: tag || "latest",
      registry: registry || "lenovo:8443"
    },
    deploy: { container_port: port }
  };
}

function fillGitForm(sample) {
  document.getElementById("gitAppName").value = sample.app_name;
  document.getElementById("gitWorkspace").value = sample.workspace;
  document.getElementById("gitRepoUrl").value = sample.source.repo_url || "";
  document.getElementById("gitRevision").value = sample.source.revision || "";
  document.getElementById("gitImageProject").value = sample.image.project;
  document.getElementById("gitImageTag").value = sample.image.tag;
  document.getElementById("gitRegistry").value = sample.image.registry;
  document.getElementById("gitPort").value = sample.deploy.container_port;
}

function fillZipForm(sample) {
  document.getElementById("zipAppName").value = sample.app_name;
  document.getElementById("zipWorkspace").value = sample.workspace;
  document.getElementById("zipUrl").value = sample.source.zip_url || "";
  document.getElementById("zipImageProject").value = sample.image.project;
  document.getElementById("zipImageTag").value = sample.image.tag;
  document.getElementById("zipRegistry").value = sample.image.registry;
  document.getElementById("zipPort").value = sample.deploy.container_port;
}

function fillLocalForm(sample) {
  document.getElementById("localAppName").value = sample.app_name;
  document.getElementById("localWorkspace").value = sample.workspace;
  document.getElementById("localPath").value = sample.source.local_path || "";
  document.getElementById("localImageProject").value = sample.image.project;
  document.getElementById("localImageTag").value = sample.image.tag;
  document.getElementById("localRegistry").value = sample.image.registry;
  document.getElementById("localPort").value = sample.deploy.container_port;
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

document.getElementById("sampleGit").onclick = () => fillGitForm(sampleGit);
document.getElementById("sampleZip").onclick = () => fillZipForm(sampleZip);
document.getElementById("sampleLocal").onclick = () => fillLocalForm(sampleLocal);

document.getElementById("healthBtn").onclick = async () => {
  await handleRequest("GET", "/healthz");
  healthCheck();
};

document.getElementById("runGitBtn").onclick = async () => {
  const payload = buildGitPayload();
  await handleRequest("POST", "/run", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload)
  });
  await refreshWorkspaces();
};

document.getElementById("runZipBtn").onclick = async () => {
  const payload = buildZipPayload();
  await handleRequest("POST", "/run", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload)
  });
  await refreshWorkspaces();
};

document.getElementById("runLocalBtn").onclick = async () => {
  const payload = buildLocalPayload();
  await handleRequest("POST", "/run", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload)
  });
  await refreshWorkspaces();
};

document.getElementById("wsRefresh").onclick = () => refreshWorkspaces();

document.getElementById("clearLog").onclick = () => {
  activityCache = [];
  renderActivity();
};

healthCheck();
refreshWorkspaces();
