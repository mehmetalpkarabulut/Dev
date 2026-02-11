# Tekton Runner: Kind + Tekton + Harbor + HTTP API (Git/Local/ZIP)

## Proje Özeti

Bu çalışma ile bir Kubernetes (kind) ortamında Tekton kuruldu ve Harbor registry ile entegre edildi. Bir HTTP servis (`tekton-runner`) sayesinde uygulama dışarıdan JSON göndererek build tetikleyebiliyor. Kaynak olarak GitHub/Azure DevOps (HTTPS token ile), yerel NFS/SMB mount ve ZIP indirip açma destekleniyor. Tekton Task, kaynak kodu alıp Kaniko ile image build ediyor ve Harbor’a pushluyor. Böylece webhook kullanmadan, dış sistemlerin JSON ile tetikleyebileceği esnek bir CI pipeline sağlanmış oluyor.

---

## 1) Önkoşullar

- Linux host
- `docker`, `kubectl`, `kind`, `curl`, `openssl`
- Harbor erişimi: `https://lenovo:8443`
- Harbor admin şifre: `Harbor12345`
- İnternet erişimi (ilk kurulum için gerekli)

---

## 2) Kind Kurulumu

```bash
kind create cluster --name tekton
kind get kubeconfig --name tekton > /tmp/kind-tekton.kubeconfig
export KUBECONFIG=/tmp/kind-tekton.kubeconfig
```

Kontrol:
```bash
kubectl get nodes
```

---

## 3) Harbor Kurulumu

```bash
cd /home/beko/harbor/harbor
sudo docker compose up -d
```

---

## 4) Harbor Sertifika (SAN)

```bash
sudo openssl req -newkey rsa:2048 -nodes -keyout /home/beko/harbor/certs/harbor.key \
  -x509 -days 365 -out /home/beko/harbor/certs/harbor.crt \
  -subj "/CN=lenovo" \
  -addext "subjectAltName=DNS:lenovo"

sudo cp /home/beko/harbor/certs/harbor.crt /home/beko/harbor/data/secret/cert/server.crt
sudo cp /home/beko/harbor/certs/harbor.key /home/beko/harbor/data/secret/cert/server.key
sudo chown 10000:10000 /home/beko/harbor/data/secret/cert/server.crt /home/beko/harbor/data/secret/cert/server.key
sudo chmod 644 /home/beko/harbor/data/secret/cert/server.crt
sudo chmod 600 /home/beko/harbor/data/secret/cert/server.key
sudo docker restart nginx
```

---

## 5) Tekton Kurulumu

```bash
kubectl apply -f https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml
kubectl apply -f https://storage.googleapis.com/tekton-releases/triggers/latest/release.yaml
kubectl apply -f https://storage.googleapis.com/tekton-releases/triggers/latest/interceptors.yaml
```

Kontrol:
```bash
kubectl get pods -n tekton-pipelines
```

---

## 6) Harbor’a Gerekli Image’leri Push (Offline çekim için)

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

## 7) Tekton Build Task (Git/Local/ZIP)

Manifest:
```
/home/beko/manifests/tekton-generic-build.yaml
```

Uygula:
```bash
kubectl apply -f /home/beko/manifests/tekton-generic-build.yaml
```

Bu Task:
- Git repo clone
- Local NFS/SMB mount
- ZIP indirip açma
- Kaniko ile build + Harbor push

Dockerfile otomatik algılama:
- ZIP içinde tek bir Dockerfile varsa otomatik bulunur
- Birden fazla Dockerfile varsa hata verir

---

## 8) Tekton Runner (HTTP API)

Kod:
```
/home/beko/tools/tekton-runner
```

Build:
```bash
cd /home/beko/tools/tekton-runner
go build -o tekton-runner ./...
```

Çalıştırma:
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

## 9) RBAC (Opsiyonel)

```bash
kubectl apply -f /home/beko/manifests/tekton-runner-rbac.yaml
```

Bu RBAC ile:
- TaskRun
- Secret
- PVC
- PV oluşturma izinleri verilir

---

## 10) JSON Örnekleri

### GitHub/Azure (HTTPS Token)

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

### ZIP (HTTP üzerinden indir)

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

## 11) Postman Ayarları

- Method: `POST`
- URL: `http://<HOST_IP>:8088/run`
- Header: `Content-Type: application/json`
- Body: JSON (yukarıdaki örneklerden biri)

---

## 12) Log ve İzleme

TaskRun listeleme:
```bash
kubectl get taskrun -n tekton-pipelines --sort-by=.metadata.creationTimestamp | tail -n 5
```

TaskRun detay:
```bash
kubectl describe taskrun -n tekton-pipelines <TASKRUN>
```

Pod logları:
```bash
kubectl logs -n tekton-pipelines pod/<POD_ADI> -c step-build
```

---

## 13) Sık Sorunlar

- `kubectl create failed`: JSON/parametre hatası olabilir
- `TaskRunResolutionFailed`: Task kurulu değilse olur
- `Dockerfile not found`: ZIP içinde Dockerfile yoksa çıkar
- `network bridge not found`: host üzerinde docker network sorunu

---

## 14) Çalışan Servis

- HTTP Server: `tekton-runner` (port 8088)
- Tekton Task: `build-and-push-generic`
- Harbor: `lenovo:8443`

