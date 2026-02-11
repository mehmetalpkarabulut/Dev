# tekton-runner (Go)

Bu araç, uygulamadan gelen JSON isteğini Tekton manifestlerine çevirir ve isteğe bağlı olarak `kubectl apply` yapar.

## Build

```bash
go build -o tekton-runner ./...
```

## Kullanım

### STDIN -> stdout

```bash
cat request.json | ./tekton-runner
```

### Dosyadan al, çıktı dizinine yaz

```bash
./tekton-runner -in request.json -out-dir /tmp/tekton
```

### Uygula

```bash
./tekton-runner -in request.json -apply
```

## JSON Şema

```json
{
  "namespace": "tekton-pipelines",
  "task": "build-and-push-generic",
  "source": {
    "type": "git|local|zip",
    "repo_url": "https://github.com/user/repo",
    "revision": "main",
    "git_username": "user",
    "git_token": "token",
    "git_secret": "optional-secret-name",
    "local_path": "projeler/myapp",
    "pvc_name": "pvc-nfs-123",
    "zip_url": "https://example.com/source.zip",
    "zip_username": "user",
    "zip_password": "pass",
    "nfs": {
      "server": "10.0.0.10",
      "path": "/exports/projects",
      "size": "50Gi"
    },
    "smb": {
      "server": "fileserver.local",
      "share": "projects",
      "username": "user",
      "password": "pass",
      "size": "50Gi",
      "volume_handle": "optional-handle",
      "secret_name": "optional-secret"
    }
  },
  "image": {
    "project": "myapp",
    "tag": "latest",
    "registry": "lenovo:8443"
  }
}
```

## Notlar

- `source.type=git` için `repo_url` zorunlu.
- `source.type=local` için `local_path` zorunlu ve `pvc_name` ya da `nfs/smb` zorunlu.
- Git kullanıcı/şifre verilirse secret otomatik oluşturulur.
- NFS/SMB bilgisi verilirse PV+PVC (ve SMB secret) otomatik oluşturulur.

## SMB Notu

SMB kullanımı için cluster'da SMB CSI driver bulunmalıdır. Yoksa PV/PVC oluşturulsa bile mount başarısız olur.

## Gerekli Tekton Önkoşulları

- `build-and-push-generic` Task kurulu olmalı.
- `build-bot` ServiceAccount ve `harbor-creds` Secret hazır olmalı.
- NFS/SMB ile PV oluşturulacaksa ClusterRole gerekir. Örnek: `manifests/tekton-runner-rbac.yaml`.

## HTTP Sunucu Modu

### Çalıştırma

```bash
./tekton-runner -server -addr :8088
```

API key ile:

```bash
./tekton-runner -server -addr :8088 -api-key YOUR_KEY
```

### Endpointler

- `GET /healthz` -> `ok`
- `POST /run` -> JSON alır, manifestleri apply eder
- `POST /run?dry_run=true` -> YAML döner

### Postman Örneği

- Method: `POST`
- URL: `http://<host>:8088/run`
- Header: `Content-Type: application/json`
- Header (opsiyonel): `Authorization: Bearer YOUR_KEY`
- Body: raw JSON (şema README üst kısmında)
