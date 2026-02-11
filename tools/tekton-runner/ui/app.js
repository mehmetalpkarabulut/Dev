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
    const card = document.createElement("div");
    card.className = "detail-card";

    const replica = status?.apps?.find?.((a) => a.app === name)?.replicas;
    const replicaText = replica !== undefined ? replica : "?";

    card.innerHTML = `<div class="detail-label">App</div><div class="detail-value">${name}</div><div class="detail-label">Replicas</div><div class="detail-value">${replicaText}</div>`;

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
    scaleInput.value = replicaText === "?" ? "1" : replicaText;
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

function buildRunPayload() {
  const appName = document.getElementById("formAppName").value.trim();
  const workspace = document.getElementById("formWorkspace").value.trim();
  const sourceType = document.getElementById("formSourceType").value;
  const repoUrl = document.getElementById("formRepoUrl").value.trim();
  const revision = document.getElementById("formRevision").value.trim();
  const gitUser = document.getElementById("formGitUser").value.trim();
  const gitToken = document.getElementById("formGitToken").value.trim();
  const zipUrl = document.getElementById("formZipUrl").value.trim();
  const localPath = document.getElementById("formLocalPath").value.trim();
  const project = document.getElementById("formImageProject").value.trim();
  const tag = document.getElementById("formImageTag").value.trim();
  const registry = document.getElementById("formRegistry").value.trim();
  const port = parseInt(document.getElementById("formPort").value, 10) || 3000;

  const source = { type: sourceType };
  if (sourceType === "git") {
    source.repo_url = repoUrl;
    source.revision = revision || "main";
    if (gitUser) source.git_username = gitUser;
    if (gitToken) source.git_token = gitToken;
  }
  if (sourceType === "zip") {
    source.zip_url = zipUrl;
  }
  if (sourceType === "local") {
    source.local_path = localPath;
  }

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

function fillForm(sample) {
  document.getElementById("formAppName").value = sample.app_name;
  document.getElementById("formWorkspace").value = sample.workspace;
  document.getElementById("formSourceType").value = sample.source.type;
  document.getElementById("formRepoUrl").value = sample.source.repo_url || "";
  document.getElementById("formRevision").value = sample.source.revision || "";
  document.getElementById("formZipUrl").value = sample.source.zip_url || "";
  document.getElementById("formLocalPath").value = sample.source.local_path || "";
  document.getElementById("formImageProject").value = sample.image.project;
  document.getElementById("formImageTag").value = sample.image.tag;
  document.getElementById("formRegistry").value = sample.image.registry;
  document.getElementById("formPort").value = sample.deploy.container_port;
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

document.getElementById("sampleGit").onclick = () => fillForm(sampleGit);
document.getElementById("sampleZip").onclick = () => fillForm(sampleZip);
document.getElementById("sampleLocal").onclick = () => fillForm(sampleLocal);

document.getElementById("healthBtn").onclick = async () => {
  await handleRequest("GET", "/healthz");
  healthCheck();
};

document.getElementById("runBtn").onclick = async () => {
  const payload = buildRunPayload();
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
