# Codex Remote Setup Runbook (Tekton Runner)

Bu dokumani Codex'e vererek baska bir makinede ayni sistemi kurdurabilirsin. Adimlar otomatik uygulanmasi icin yazildi.

## Hedef

- Docker, kubectl, kind kurulsun
- Harbor kurulsun
- Tekton kurulsun
- Tekton build task ve gerekli imajlar Harbor'a yuklensin
- Tekton Runner (Go) build edilip HTTP servis olarak calissin

## Varsayimlar

- Linux (Ubuntu) kullaniliyor
- Harbor hostname: `lenovo`, port: `8443`
- GitHub repo: `mehmetalpkarabulut/Dev`
- Kubeconfig: `/tmp/kind-tekton.kubeconfig`
- HTTP servis: `0.0.0.0:8088`

## Adim 1: Sistem Paketleri

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl gnupg lsb-release git
```

## Adim 2: Docker Kurulumu

```bash
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

## Adim 3: kubectl + kind

```bash
sudo apt-get install -y kubectl
curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.23.0/kind-linux-amd64
chmod +x ./kind
sudo mv ./kind /usr/local/bin/kind
```

## Adim 4: Kind Cluster

```bash
kind create cluster --name tekton
kind get kubeconfig --name tekton > /tmp/kind-tekton.kubeconfig
export KUBECONFIG=/tmp/kind-tekton.kubeconfig
kubectl get nodes
```

## Adim 5: Harbor Kurulumu

```bash
HARBOR_VERSION=2.10.0
cd /tmp
wget https://github.com/goharbor/harbor/releases/download/v${HARBOR_VERSION}/harbor-offline-installer-v${HARBOR_VERSION}.tgz
tar -xzf harbor-offline-installer-v${HARBOR_VERSION}.tgz
cd /tmp/harbor
cp harbor.yml.tmpl harbor.yml
# hostname: lenovo
# https port: 8443
sudo ./install.sh
```

Harbor ayakta mi:
```bash
sudo docker ps --format 'table {{.Names}}\t{{.Status}}' | rg -i 'harbor|nginx|registry|core|portal|jobservice|db|redis'
```

## Adim 6: Harbor Sertifika (SAN)

```bash
sudo openssl req -newkey rsa:2048 -nodes -keyout /home/$USER/harbor/certs/harbor.key \
  -x509 -days 365 -out /home/$USER/harbor/certs/harbor.crt \
  -subj "/CN=lenovo" \
  -addext "subjectAltName=DNS:lenovo"

sudo cp /home/$USER/harbor/certs/harbor.crt /home/$USER/harbor/data/secret/cert/server.crt
sudo cp /home/$USER/harbor/certs/harbor.key /home/$USER/harbor/data/secret/cert/server.key
sudo chown 10000:10000 /home/$USER/harbor/data/secret/cert/server.crt /home/$USER/harbor/data/secret/cert/server.key
sudo chmod 644 /home/$USER/harbor/data/secret/cert/server.crt
sudo chmod 600 /home/$USER/harbor/data/secret/cert/server.key
sudo docker restart nginx
```

## Adim 7: Tekton Kurulumu

```bash
kubectl apply -f https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml
kubectl apply -f https://storage.googleapis.com/tekton-releases/triggers/latest/release.yaml
kubectl apply -f https://storage.googleapis.com/tekton-releases/triggers/latest/interceptors.yaml
```

## Adim 8: Tekton Pod Kontrol

```bash
kubectl get pods -n tekton-pipelines
```

## Adim 9: Gerekli Image'leri Harbor'a Yukle

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

## Adim 10: Repo Klonla (Tekton Task + Runner)

```bash
git clone git@github.com:mehmetalpkarabulut/Dev.git
cd Dev
```

## Adim 11: Tekton Task + RBAC Uygula

```bash
kubectl apply -f manifests/tekton-generic-build.yaml
kubectl apply -f manifests/tekton-runner-rbac.yaml
```

## Adim 12: Tekton Runner Build + Run

```bash
cd tools/tekton-runner
sudo apt-get install -y golang-go

go build -o tekton-runner ./...
./tekton-runner -server -addr 0.0.0.0:8088 -host-ip <HOST_IP>
```

Health check:
```
http://<HOST_IP>:8088/healthz
```

Run endpoint:
```
http://<HOST_IP>:8088/run
```

Endpoint sorgulama:
```
http://<HOST_IP>:8088/endpoint?workspace=ws-<app_name>&app=<app_name>
```

Workspace listesi:
```
http://<HOST_IP>:8088/workspaces
```

## Ornek ZIP JSON (workspace + otomatik kind)

```json
{
  "app_name": "demoapp",
  "source": {
    "type": "zip",
    "zip_url": "https://example.com/app.zip",
    "zip_username": "ZIP_USER",
    "zip_password": "ZIP_PASS"
  },
  "image": {
    "project": "demoapp",
    "tag": "latest",
    "registry": "lenovo:8443"
  },
  "deploy": {
    "container_port": 8080
  }
}
```
