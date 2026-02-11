const consoleEl = document.getElementById("console");
const healthBadge = document.getElementById("healthBadge");
const runnerBase = document.getElementById("runnerBase");

function log(obj) {
  const text = typeof obj === "string" ? obj : JSON.stringify(obj, null, 2);
  consoleEl.textContent = text + "\n" + consoleEl.textContent;
}

async function api(path, options = {}) {
  const res = await fetch(`/api${path}`, options);
  const contentType = res.headers.get("content-type") || "";
  const body = contentType.includes("application/json") ? await res.json() : await res.text();
  return { status: res.status, body };
}

async function healthCheck() {
  try {
    const { status } = await api("/healthz");
    healthBadge.textContent = `HEALTH: ${status}`;
    healthBadge.style.color = status === 200 ? "#35c2aa" : "#ff6b6b";
  } catch (err) {
    healthBadge.textContent = "HEALTH: ERR";
    healthBadge.style.color = "#ff6b6b";
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

runnerBase.textContent = "/api → RUNNER_BASE_URL";

// Buttons

document.getElementById("healthBtn").onclick = async () => {
  const res = await api("/healthz");
  log({ endpoint: "/healthz", ...res });
  healthCheck();
};

document.getElementById("runBtn").onclick = async () => {
  const body = document.getElementById("runBody").value;
  const res = await api("/run", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body
  });
  log({ endpoint: "/run", ...res });
};

document.getElementById("epBtn").onclick = async () => {
  const ws = document.getElementById("epWorkspace").value.trim();
  const app = document.getElementById("epApp").value.trim();
  const res = await api(`/endpoint?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`);
  log({ endpoint: "/endpoint", ...res });
};

document.getElementById("wsList").onclick = async () => {
  const res = await api("/workspaces");
  log({ endpoint: "/workspaces", ...res });
};

document.getElementById("wsStatus").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  const res = await api(`/workspace/status?workspace=${encodeURIComponent(ws)}`);
  log({ endpoint: "/workspace/status", ...res });
};

document.getElementById("wsDelete").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  const res = await api(`/workspace/delete?workspace=${encodeURIComponent(ws)}`, { method: "POST" });
  log({ endpoint: "/workspace/delete", ...res });
};

document.getElementById("wsRestart").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  const res = await api(`/workspace/restart?workspace=${encodeURIComponent(ws)}`, { method: "POST" });
  log({ endpoint: "/workspace/restart", ...res });
};

document.getElementById("wsScale").onclick = async () => {
  const ws = document.getElementById("wsName").value.trim();
  const app = document.getElementById("wsScaleApp").value.trim();
  const rep = document.getElementById("wsScaleRep").value.trim();
  const res = await api(`/workspace/scale?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}&replicas=${encodeURIComponent(rep)}`, { method: "POST" });
  log({ endpoint: "/workspace/scale", ...res });
};

document.getElementById("appStatus").onclick = async () => {
  const ws = document.getElementById("appWs").value.trim();
  const app = document.getElementById("appName").value.trim();
  const res = await api(`/app/status?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`);
  log({ endpoint: "/app/status", ...res });
};

document.getElementById("appRestart").onclick = async () => {
  const ws = document.getElementById("appWs").value.trim();
  const app = document.getElementById("appName").value.trim();
  const res = await api(`/app/restart?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`, { method: "POST" });
  log({ endpoint: "/app/restart", ...res });
};

document.getElementById("appDelete").onclick = async () => {
  const ws = document.getElementById("appWs").value.trim();
  const app = document.getElementById("appName").value.trim();
  const res = await api(`/app/delete?workspace=${encodeURIComponent(ws)}&app=${encodeURIComponent(app)}`, { method: "POST" });
  log({ endpoint: "/app/delete", ...res });
};

healthCheck();
