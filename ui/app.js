const healthBadge = document.getElementById("healthBadge");
const runnerBase = document.getElementById("runnerBase");
const activityEl = document.getElementById("activity");

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
  await handleRequest("GET", `/endpoint?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`);
};

document.getElementById("wsList").onclick = async () => {
  await handleRequest("GET", "/workspaces");
};

document.getElementById("wsStatus").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  await handleRequest("GET", `/workspace/status?workspace=${encodeURIComponent(ws)}`);
};

document.getElementById("wsDelete").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  await handleRequest("POST", `/workspace/delete?workspace=${encodeURIComponent(ws)}`, { method: "POST" });
};

document.getElementById("wsRestart").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  await handleRequest("POST", `/workspace/restart?workspace=${encodeURIComponent(ws)}`, { method: "POST" });
};

document.getElementById("wsScale").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  const app = document.getElementById("wsScaleApp").value.trim();
  const rep = document.getElementById("wsScaleRep").value.trim();
  await handleRequest("POST", `/workspace/scale?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}&replicas=${encodeURIComponent(rep)}`, { method: "POST" });
};

document.getElementById("appStatus").onclick = async () => {
  const ws = document.getElementById("appWs").value.trim();
  const app = document.getElementById("appName").value.trim();
  await handleRequest("GET", `/app/status?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`);
};

document.getElementById("appRestart").onclick = async () => {
  const ws = document.getElementById("appWs").value.trim();
  const app = document.getElementById("appName").value.trim();
  await handleRequest("POST", `/app/restart?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`, { method: "POST" });
};

document.getElementById("appDelete").onclick = async () => {
  const ws = document.getElementById("appWs").value.trim();
  const app = document.getElementById("appName").value.trim();
  await handleRequest("POST", `/app/delete?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`, { method: "POST" });
};

document.getElementById("clearLog").onclick = () => {
  activityEl.innerHTML = "";
};

healthCheck();
