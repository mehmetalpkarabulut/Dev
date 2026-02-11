# DevOps/CI Runner - Kaldigimiz Yer ve Devam Adimlari

Bu dokuman, Codex oturumunu kapattiktan sonra kaldigimiz yerden devam edebilmeniz icin ozet + adim adim devam talimatidir.

## 1) Son Durum Ozeti (2026-02-11)

### Basarili Olanlar
- `tekton-runner` server calisiyor.
- Tekton + Harbor + kind tek cluster yapisi calisiyor.
- Harbordaki image build + push isleri calisiyor.
- Yeni workspace icin otomatik kind cluster olusturma **calisiyor** (file descriptor sorunu cozuldu).
- UI aktif ve calisiyor.

### Son Kalan Problem
Yeni workspace (ornek `ws-memo-test`) uzerinde deploy olusuyor, fakat pod `ImagePullBackOff` veriyor:

```
failed to pull image ... tls: failed to verify certificate: x509: certificate signed by unknown authority
```

Bu, **workspace cluster icinde containerd `certs.d` config_path kullanmadigi** icin oluyor. Tekton runner'a otomatik fix eklendi ama yeni cluster'da uygulanip uygulanmadigi kontrol edilmeli.

## 2) Calisan Servisler

### Tekton runner
```
nohup /home/beko/tools/tekton-runner/tekton-runner \
  -server -addr 0.0.0.0:8088 \
  -host-ip 10.134.70.220 \
  -kubeconfig /tmp/kind-tekton.kubeconfig \
  > /tmp/tekton-runner.log 2>&1 &
```

Log:
```
/tmp/tekton-runner.log
```

### Harbor
```
cd /home/beko/harbor/harbor
sudo docker compose up -d
```

Health kontrol:
```
curl -sk -o /dev/null -w "%{http_code}\n" https://lenovo:8443/api/v2.0/health
```

### Tekton cluster (ana)
Kubeconfig:
```
/tmp/kind-tekton.kubeconfig
```

## 3) Sorunu Cozen Sistem Ayarlari (Kalici)

### File descriptor limit
```
/etc/systemd/system/docker.service.d/limits.conf
```
Icerik:
```
[Service]
LimitNOFILE=1048576
```

Aktif etmek icin:
```
sudo systemctl daemon-reload
sudo systemctl restart docker
```

### inotify limitleri
```
/etc/sysctl.d/99-inotify.conf
```
Icerik:
```
fs.inotify.max_user_instances=8192
fs.inotify.max_user_watches=1048576
```

Uygulamak icin:
```
sudo sysctl --system
```

## 4) Kalan Problem: Yeni Workspace ImagePullBackOff

### Semptom
Pod `ImagePullBackOff`:
```
Failed to pull image ... tls: failed to verify certificate
```

### Hedef
Yeni workspace cluster icinde `containerd` **/etc/containerd/certs.d** dizinini **okuyacak**.

### Runner tarafinda yapilan degisiklik
`/home/beko/tools/tekton-runner/main.go` icinde `configureKindNode()` fonksiyonu guncellendi:
- `hosts.toml` yaziliyor
- `config_path = "/etc/containerd/certs.d"` ekleniyor
- `systemctl restart containerd` yapiliyor

### Dogrulama
Yeni cluster olustuktan sonra node icinde kontrol:
```
sudo docker exec ws-memo-test-control-plane sh -c "grep -n 'config_path' /etc/containerd/config.toml"
```
Beklenen:
```
config_path = "/etc/containerd/certs.d"
```

### Manuel Fix (gerekiyorsa)
```
sudo docker exec ws-memo-test-control-plane sh -c "mkdir -p /etc/containerd/certs.d/lenovo:8443"
cat > /tmp/lenovo8443-hosts.toml <<'EOF'
server = "https://lenovo:8443"

[host."https://lenovo:8443"]
  capabilities = ["pull", "resolve"]
  skip_verify = true
EOF
sudo docker cp /tmp/lenovo8443-hosts.toml ws-memo-test-control-plane:/etc/containerd/certs.d/lenovo:8443/hosts.toml
sudo docker exec ws-memo-test-control-plane sh -c "grep -q 'config_path = \"/etc/containerd/certs.d\"' /etc/containerd/config.toml || printf '\n[plugins.\"io.containerd.grpc.v1.cri\".registry]\n  config_path = \"/etc/containerd/certs.d\"\n' >> /etc/containerd/config.toml"
sudo docker exec ws-memo-test-control-plane sh -c "systemctl restart containerd"
```

Sonra pod'u yeniden olustur:
```
sudo KUBECONFIG=/tmp/kind-ws-memo-test.kubeconfig kubectl delete pod -n ws-memo-test -l app=memo-app-test
```

## 5) Yeni workspace tetikleme akisi
1. UI'dan `git build`/`zip build` trigger edilir
2. Runner yeni kind cluster olusturur
3. Namespace + deployment + service apply edilir
4. `lenovo:8443/<app>/<app>:latest` image cekilir

## 6) Runner Kodlari (Guncel Kaynak)
- Repo icinde: `/home/beko/Dev/tools/tekton-runner`
- Gunluk kod: `/home/beko/tools/tekton-runner`

Son degisiklikler:
- `configureKindNode()` icinde containerd config_path + restart eklenmis durumda
- Binary yeniden build edildi

## 7) Devam Adimlari
1. UI'dan yeni workspace tetikle
2. Pod `ImagePullBackOff` olursa yukaridaki config_path kontrol et
3. Sorun devam ederse runner logunu kontrol et:
```
/tmp/tekton-runner.log
```

## 8) Faydalı Komutlar

Harbor ayakta mi:
```
sudo docker ps --format 'table {{.Names}}\t{{.Status}}' | rg -i 'harbor|nginx|registry'
```

Workspace cluster listesi:
```
sudo kind get clusters
```

Pod durumlari (workspace icinde):
```
sudo KUBECONFIG=/tmp/kind-ws-<workspace>.kubeconfig kubectl get pods -n ws-<workspace>
```
