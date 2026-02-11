# Sifirdan Kurulum: Kind + Tekton + Harbor + HTTP Tekton Runner (Git/Local/ZIP)

Bu dokuman, sifirdan hic bilginiz yokmus gibi tum adimlari ve gerekli manifestleri icerir. Amaç: 
- Kind uzerinde Kubernetes kurmak
- Tekton kurmak
- Harbor registry kurmak
- Harici bir uygulamanin HTTP ile JSON gondererek Tekton TaskRun tetiklemesi
- Kaynak olarak Git (GitHub/Azure), Local NFS/SMB ve ZIP indirip acma destegi
- Docker image olusturup Harbor'a pushlamak

## Icerik

1. Sistem gereksinimleri
2. Docker kurulumu (Ubuntu)
3. kubectl kurulumu
4. kind kurulumu
5. Harbor kurulumu
6. Harbor sertifika (SAN) ve registry hazirligi
7. Tekton kurulumu
8. Tekton build task ve destek imajlarini Harbor'a yukleme
9. Tekton Task manifesti (Git/Local/ZIP destekli)
10. Tekton Runner (Go) kurulumu
11. Tekton Runner HTTP servisini calistirma
12. RBAC (opsiyonel)
13. Postman ve JSON ornekleri
14. Log / debug komutlari
15. Sik hata/ceyiz

---

## 1) Sistem Gereksinimleri

- Ubuntu (20.04+ tavsiye)
- Root veya sudo yetkisi
- En az 4 CPU / 8 GB RAM / 30 GB disk
- Internet erisimi (ilk kurulum icin)

---

## 2) Docker Kurulumu (Ubuntu)

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl gnupg lsb-release

sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg

echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo $VERSION_CODENAME) stable" | \
  sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
```

Docker test:
```bash
sudo docker run --rm hello-world
```

---

## 3) kubectl Kurulumu

```bash
sudo apt-get update
sudo apt-get install -y kubectl
```

---

## 4) kind Kurulumu

```bash
curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.23.0/kind-linux-amd64
chmod +x ./kind
sudo mv ./kind /usr/local/bin/kind
```

Kind cluster:
```bash
kind create cluster --name tekton
kind get kubeconfig --name tekton > /tmp/kind-tekton.kubeconfig
export KUBECONFIG=/tmp/kind-tekton.kubeconfig
kubectl get nodes
```

---

## 5) Harbor Kurulumu

Harbor klasoru:
```
/home/beko/harbor/harbor
```

Calistirma:
```bash
cd /home/beko/harbor/harbor
sudo docker compose up -d
```

---

## 6) Harbor Sertifika (SAN) ve Registry Hazirligi

SAN iceren sertifika olustur:
```bash
sudo openssl req -newkey rsa:2048 -nodes -keyout /home/beko/harbor/certs/harbor.key \
  -x509 -days 365 -out /home/beko/harbor/certs/harbor.crt \
  -subj "/CN=lenovo" \
  -addext "subjectAltName=DNS:lenovo"
```

Harbor nginx sertifikasini guncelle:
```bash
sudo cp /home/beko/harbor/certs/harbor.crt /home/beko/harbor/data/secret/cert/server.crt
sudo cp /home/beko/harbor/certs/harbor.key /home/beko/harbor/data/secret/cert/server.key
sudo chown 10000:10000 /home/beko/harbor/data/secret/cert/server.crt /home/beko/harbor/data/secret/cert/server.key
sudo chmod 644 /home/beko/harbor/data/secret/cert/server.crt
sudo chmod 600 /home/beko/harbor/data/secret/cert/server.key
sudo docker restart nginx
```

Harbor projesi (opsiyonel):
```bash
sudo curl -sk -u 'admin:Harbor12345' -H 'Content-Type: application/json' \
  -d '{"project_name":"tektoncd","public":true}' \
  https://lenovo:8443/api/v2.0/projects
```

---

## 7) Tekton Kurulumu

```bash
kubectl apply -f https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml
kubectl apply -f https://storage.googleapis.com/tekton-releases/triggers/latest/release.yaml
kubectl apply -f https://storage.googleapis.com/tekton-releases/triggers/latest/interceptors.yaml
```

Kontrol:
```bash
kubectl get pods -n tekton-pipelines
```

Pod Security etiketi:
```bash
kubectl label namespace tekton-pipelines pod-security.kubernetes.io/enforce=baseline --overwrite
```

---

## 8) Harbor'a Gerekli Image'leri Yukleme

Bu sayede Tekton podlari internet olmadan Harbor'dan cekebilir:

```bash
sudo docker pull node:18-alpine
sudo docker pull alpine/git:2.45.2
sudo docker pull gcr.io/kaniko-project/executor:debug
sudo docker pull curlimages/curl:8.12.1
sudo docker pull python:3.12-alpine

sudo docker tag node:18-alpine lenovo:8443/library/node:18-alpine
sudo docker tag alpine/git:2.45.2 lenovo:8443/library/alpine-git:2.45.2
sudo docker tag gcr.io/kaniko-project/executor:debug lenovo:8443/library/kaniko-executor:debug
sudo docker tag curlimages/curl:8.12.1 lenovo:8443/library/curl:8.12.1
sudo docker tag python:3.12-alpine lenovo:8443/library/python:3.12-alpine

sudo docker push lenovo:8443/library/node:18-alpine
sudo docker push lenovo:8443/library/alpine-git:2.45.2
sudo docker push lenovo:8443/library/kaniko-executor:debug
sudo docker push lenovo:8443/library/curl:8.12.1
sudo docker push lenovo:8443/library/python:3.12-alpine
```

---

## 9) Tekton Secret ve ServiceAccount

Harbor credentials secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: harbor-creds
  namespace: tekton-pipelines
type: kubernetes.io/dockerconfigjson
stringData:
  .dockerconfigjson: |
    {"auths":{"lenovo:8443":{"username":"admin","password":"Harbor12345","auth":"YWRtaW46SGFyYm9yMTIzNDU="}}}
```

ServiceAccount:
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: build-bot
  namespace: tekton-pipelines
secrets:
  - name: harbor-creds
```

Uygula:
```bash
kubectl apply -f /path/to/harbor-creds.yaml
kubectl apply -f /path/to/build-bot.yaml
```

---

## 10) Tekton Task (Git/Local/ZIP)

Dosya:
```
/home/beko/manifests/tekton-generic-build.yaml
```

Uygula:
```bash
kubectl apply -f /home/beko/manifests/tekton-generic-build.yaml
```

Manifest (tam hali):

```yaml
apiVersion: tekton.dev/v1
kind: Task
metadata:
  name: build-and-push-generic
  namespace: tekton-pipelines
spec:
  params:
    - name: source-type
      type: string
      description: "git, local, or zip"
    - name: repo-url
      type: string
      default: ""
    - name: revision
      type: string
      default: main
    - name: project
      type: string
      description: "Harbor project name (lowercase will be used)"
    - name: registry
      type: string
      default: lenovo:8443
    - name: tag
      type: string
      default: latest
    - name: local-path
      type: string
      default: ""
    - name: zip-url
      type: string
      default: ""
    - name: zip-username
      type: string
      default: ""
    - name: zip-password
      type: string
      default: ""
  workspaces:
    - name: source
    - name: git-credentials
      optional: true
    - name: local-source
      optional: true
  steps:
    - name: prepare-git
      image: lenovo:8443/library/alpine-git:2.45.2
      script: |
        set -e
        if [ "$(params.source-type)" != "git" ]; then
          exit 0
        fi
        rm -rf /workspace/source/*
        if [ -z "$(params.repo-url)" ]; then
          echo "repo-url param is required for git"
          exit 1
        fi
        url="$(params.repo-url)"
        user=""
        token=""
        if [ -f /workspace/git-credentials/username ]; then
          user=$(cat /workspace/git-credentials/username)
        fi
        if [ -f /workspace/git-credentials/token ]; then
          token=$(cat /workspace/git-credentials/token)
        fi
        if [ -n "$user" ] && [ -n "$token" ]; then
          authed_url="${url/https:\/\//https://${user}:${token}@}"
        else
          authed_url="$url"
        fi
        ref="$(params.revision)"
        ref="${ref#refs/heads/}"
        git clone --depth 1 --branch "$ref" "$authed_url" /workspace/source
    - name: prepare-local
      image: lenovo:8443/library/alpine-git:2.45.2
      script: |
        set -e
        if [ "$(params.source-type)" != "local" ]; then
          exit 0
        fi
        rm -rf /workspace/source/*
        if [ -z "$(params.local-path)" ]; then
          echo "local-path param is required for local"
          exit 1
        fi
        src="/workspace/local-source/$(params.local-path)"
        if [ ! -d "$src" ]; then
          echo "local path not found: $src"
          exit 1
        fi
        cp -a "$src/." /workspace/source/
    - name: prepare-zip
      image: lenovo:8443/library/python:3.12-alpine
      script: |
        set -e
        if [ "$(params.source-type)" != "zip" ]; then
          exit 0
        fi
        if [ -z "$(params.zip-url)" ]; then
          echo "zip-url param is required for zip"
          exit 1
        fi
        rm -rf /workspace/source/*
        python - <<'PY'
        import os, urllib.request, zipfile, base64

        url = os.environ.get("ZIP_URL")
        user = os.environ.get("ZIP_USER")
        pw = os.environ.get("ZIP_PASS")

        req = urllib.request.Request(url)
        if user and pw:
            token = base64.b64encode(f"{user}:{pw}".encode()).decode()
            req.add_header("Authorization", f"Basic {token}")

        with urllib.request.urlopen(req) as r:
            data = r.read()

        zip_path = "/tmp/src.zip"
        with open(zip_path, "wb") as f:
            f.write(data)

        dst = "/workspace/source"
        with zipfile.ZipFile(zip_path, "r") as z:
            z.extractall(dst)
        PY
        # Auto-detect Dockerfile location
        found="$(find /workspace/source -type f -name Dockerfile | head -n 2)"
        count="$(printf '%s\n' "$found" | grep -c . || true)"
        if [ "$count" -eq 0 ]; then
          echo "Dockerfile not found in zip"
          exit 1
        fi
        if [ "$count" -gt 1 ]; then
          echo "Multiple Dockerfile files found; please provide a zip with a single Dockerfile"
          printf '%s\n' "$found"
          exit 1
        fi
        df="$(printf '%s\n' "$found" | head -n 1)"
        ctx="$(dirname "$df")"
        echo "$df" > /workspace/source/.dockerfile-path
        echo "$ctx" > /workspace/source/.context-path
      env:
        - name: ZIP_URL
          value: $(params.zip-url)
        - name: ZIP_USER
          value: $(params.zip-username)
        - name: ZIP_PASS
          value: $(params.zip-password)
    - name: create-project
      image: lenovo:8443/library/curl:8.12.1
      script: |
        set -e
        proj="$(params.project)"
        proj="$(printf '%s' "$proj" | tr '[:upper:]' '[:lower:]')"
        code=$(curl -sk -o /dev/null -w "%{http_code}" \
          -u admin:Harbor12345 \
          -H 'Content-Type: application/json' \
          -d '{"project_name":"'"$proj"'","metadata":{"public":"true"}}' \
          https://lenovo:8443/api/v2.0/projects || true)
        if [ "$code" != "201" ] && [ "$code" != "409" ]; then
          echo "Failed to create project. HTTP $code"
          exit 1
        fi
    - name: build
      image: lenovo:8443/library/kaniko-executor:debug
      script: |
        set -e
        dockerfile="/workspace/source/Dockerfile"
        context="/workspace/source"
        if [ -f /workspace/source/.dockerfile-path ]; then
          dockerfile="$(cat /workspace/source/.dockerfile-path)"
        fi
        if [ -f /workspace/source/.context-path ]; then
          context="$(cat /workspace/source/.context-path)"
        fi
        proj="$(params.project)"
        proj="$(printf '%s' "$proj" | tr '[:upper:]' '[:lower:]')"
        dest="$(params.registry)/${proj}/${proj}:$(params.tag)"
        /kaniko/executor \
          --dockerfile="$dockerfile" \
          --context="$context" \
          --destination="${dest}" \
          --skip-tls-verify \
          --skip-tls-verify-pull
      volumeMounts:
        - name: docker-config
          mountPath: /kaniko/.docker
  volumes:
    - name: docker-config
      secret:
        secretName: harbor-creds
        items:
          - key: .dockerconfigjson
            path: config.json
```

---

## 11) Tekton Runner (HTTP API) - Kurulum

Kod dizini:
```
/home/beko/tools/tekton-runner
```

Go kurulumu:
```bash
sudo apt-get update
sudo apt-get install -y golang-go
```

Build:
```bash
cd /home/beko/tools/tekton-runner
go build -o tekton-runner ./...
```

---

## 12) Tekton Runner HTTP Servisini Calistirma

```bash
./tekton-runner -server -addr 0.0.0.0:8088
```

Health check:
```
http://<HOST_IP>:8088/healthz
```

Run endpoint:
```
http://<HOST_IP>:8088/run
```

---

## 13) RBAC (Opsiyonel)

```bash
kubectl apply -f /home/beko/manifests/tekton-runner-rbac.yaml
```

RBAC manifest:
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tekton-runner-sa
  namespace: tekton-pipelines
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: tekton-runner-role
  namespace: tekton-pipelines
rules:
  - apiGroups: ["tekton.dev"]
    resources: ["taskruns"]
    verbs: ["create", "get", "list", "watch", "delete"]
  - apiGroups: [""]
    resources: ["secrets", "persistentvolumeclaims"]
    verbs: ["create", "get", "list", "watch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: tekton-runner-rb
  namespace: tekton-pipelines
subjects:
  - kind: ServiceAccount
    name: tekton-runner-sa
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: tekton-runner-role
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: tekton-runner-pv-role
rules:
  - apiGroups: [""]
    resources: ["persistentvolumes"]
    verbs: ["create", "get", "list", "watch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: tekton-runner-pv-rb
subjects:
  - kind: ServiceAccount
    name: tekton-runner-sa
    namespace: tekton-pipelines
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: tekton-runner-pv-role
```

---

## 14) Postman / JSON Ornekleri

### Git (GitHub/Azure HTTPS)

```json
{
  "source": {
    "type": "git",
    "repo_url": "https://github.com/mehmetalpkarabulut/Dev",
    "revision": "main",
    "git_username": "GITHUB_USER",
    "git_token": "GITHUB_TOKEN"
  },
  "image": {
    "project": "dev",
    "tag": "latest",
    "registry": "lenovo:8443"
  }
}
```

### Local NFS

```json
{
  "source": {
    "type": "local",
    "local_path": "projeler/myapp",
    "nfs": {
      "server": "10.0.0.10",
      "path": "/exports/projects",
      "size": "50Gi"
    }
  },
  "image": {
    "project": "myapp",
    "tag": "latest",
    "registry": "lenovo:8443"
  }
}
```

### Local SMB

```json
{
  "source": {
    "type": "local",
    "local_path": "projeler/myapp",
    "smb": {
      "server": "fileserver.local",
      "share": "projects",
      "username": "SMB_USER",
      "password": "SMB_PASS",
      "size": "50Gi"
    }
  },
  "image": {
    "project": "myapp",
    "tag": "latest",
    "registry": "lenovo:8443"
  }
}
```

### ZIP Indir ve Build

```json
{
  "source": {
    "type": "zip",
    "zip_url": "https://example.com/app.zip",
    "zip_username": "ZIP_USER",
    "zip_password": "ZIP_PASS"
  },
  "image": {
    "project": "myapp",
    "tag": "latest",
    "registry": "lenovo:8443"
  }
}
```

---

## 15) Log / Debug Komutlari

TaskRun listeleme:
```bash
kubectl get taskrun -n tekton-pipelines --sort-by=.metadata.creationTimestamp | tail -n 5
```

TaskRun detayi:
```bash
kubectl describe taskrun -n tekton-pipelines <TASKRUN>
```

Pod loglari:
```bash
kubectl get pods -n tekton-pipelines -l tekton.dev/taskRun=<TASKRUN>
kubectl logs -n tekton-pipelines pod/<POD_ADI> -c step-build
```

---

## 16) Sik Hatalar

- `TaskRunResolutionFailed`: Task bulunamiyor. Task manifestini apply et.
- `Dockerfile not found`: ZIP icinde Dockerfile yok.
- `Multiple Dockerfile`: ZIP icinde birden fazla Dockerfile var.
- `kubectl create failed`: JSON/parametre hatasi.
- `network bridge not found`: Docker network sorunu.

---

## 17) Servislerin Ozeti

- Tekton Task: `build-and-push-generic`
- Tekton Runner API: `http://<HOST_IP>:8088/run`
- Harbor: `https://lenovo:8443`

