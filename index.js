const http = require("http");
const https = require("https");
const fs = require("fs");
const path = require("path");

const PORT = process.env.PORT || 3000;
const HOST = process.env.HOST || "127.0.0.1";
const RUNNER_BASE_URL = process.env.RUNNER_BASE_URL || "http://127.0.0.1:8088";
const UI_DIR = path.join(__dirname, "ui");

function send(res, status, headers, body) {
  res.writeHead(status, headers);
  res.end(body);
}

function serveStatic(req, res) {
  const urlPath = req.url === "/" ? "/index.html" : req.url;
  const filePath = path.join(UI_DIR, urlPath);
  if (!filePath.startsWith(UI_DIR)) {
    return send(res, 404, { "Content-Type": "text/plain" }, "Not found");
  }
  fs.readFile(filePath, (err, data) => {
    if (err) {
      return send(res, 404, { "Content-Type": "text/plain" }, "Not found");
    }
    const ext = path.extname(filePath);
    const mime =
      ext === ".html"
        ? "text/html"
        : ext === ".css"
        ? "text/css"
        : ext === ".js"
        ? "application/javascript"
        : "application/octet-stream";
    send(res, 200, { "Content-Type": mime }, data);
  });
}

function proxyToRunner(req, res) {
  const targetUrl = new URL(req.url.replace(/^\/api/, ""), RUNNER_BASE_URL);
  const isHttps = targetUrl.protocol === "https:";
  const client = isHttps ? https : http;

  const options = {
    method: req.method,
    hostname: targetUrl.hostname,
    port: targetUrl.port || (isHttps ? 443 : 80),
    path: targetUrl.pathname + targetUrl.search,
    headers: { ...req.headers, host: targetUrl.host }
  };

  const upstream = client.request(options, (upstreamRes) => {
    res.writeHead(upstreamRes.statusCode || 502, upstreamRes.headers);
    upstreamRes.pipe(res);
  });

  upstream.on("error", (err) => {
    send(
      res,
      502,
      { "Content-Type": "application/json" },
      JSON.stringify({ error: "proxy_failed", message: err.message })
    );
  });

  req.pipe(upstream);
}

const server = http.createServer((req, res) => {
  if (req.url.startsWith("/api/")) {
    return proxyToRunner(req, res);
  }
  return serveStatic(req, res);
});

server.listen(PORT, HOST, () => {
  console.log(`UI server running on ${HOST}:${PORT}`);
  console.log(`Runner base: ${RUNNER_BASE_URL}`);
});
