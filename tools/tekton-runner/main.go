package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"
)

type Input struct {
	Namespace string `json:"namespace"`
	Task      string `json:"task"`
	AppName   string `json:"app_name"`
	Deploy    Deploy `json:"deploy"`
	Source    Source `json:"source"`
	Image     Image  `json:"image"`
}

type Source struct {
	Type        string    `json:"type"`
	RepoURL     string    `json:"repo_url"`
	Revision    string    `json:"revision"`
	GitUsername string    `json:"git_username"`
	GitToken    string    `json:"git_token"`
	GitSecret   string    `json:"git_secret"`
	LocalPath   string    `json:"local_path"`
	PVCName     string    `json:"pvc_name"`
	ZipURL      string    `json:"zip_url"`
	ZipUsername string    `json:"zip_username"`
	ZipPassword string    `json:"zip_password"`
	NFS         *NFSConfig `json:"nfs"`
	SMB         *SMBConfig `json:"smb"`
}

type NFSConfig struct {
	Server string `json:"server"`
	Path   string `json:"path"`
	Size   string `json:"size"`
}

type SMBConfig struct {
	Server       string `json:"server"`
	Share        string `json:"share"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	Size         string `json:"size"`
	VolumeHandle string `json:"volume_handle"`
	SecretName   string `json:"secret_name"`
}

type Image struct {
	Project  string `json:"project"`
	Tag      string `json:"tag"`
	Registry string `json:"registry"`
}

type Deploy struct {
	ContainerPort int `json:"container_port"`
}

type RenderContext struct {
	Namespace   string
	Task        string
	SourceType  string
	RepoURL     string
	Revision    string
	LocalPath   string
	Project     string
	Tag         string
	Registry    string
	ZipURL      string
	ZipUsername string
	ZipPassword string
	GitSecret   string
	PVCName     string
	HasGit      bool
	HasLocal    bool
	HasZip      bool
}

type ServerState struct {
	mu        sync.Mutex
	endpoints map[string]string
}

var serverHostIP string
var serverState = &ServerState{endpoints: map[string]string{}}

func main() {
	inPath := flag.String("in", "", "input JSON file (default: stdin)")
	outDir := flag.String("out-dir", "", "output directory (default: stdout only)")
	apply := flag.Bool("apply", false, "kubectl apply generated manifests")
	server := flag.Bool("server", false, "run HTTP server")
	addr := flag.String("addr", ":8088", "server listen address")
	apiKey := flag.String("api-key", "", "optional API key for server auth (Bearer)")
	hostIP := flag.String("host-ip", "", "host IP for endpoint generation (optional)")
	flag.Parse()

	if *server {
		serverHostIP = *hostIP
		runServer(*addr, *apiKey)
		return
	}

	var data []byte
	var err error
	if *inPath == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(*inPath)
	}
	if err != nil {
		fatal("read input", err)
	}

	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("parse JSON", err)
	}

	manifests, err := buildManifests(&in)
	if err != nil {
		fatal("validate input", err)
	}

	if *outDir != "" {
		if err := os.MkdirAll(*outDir, 0o755); err != nil {
			fatal("mkdir out-dir", err)
		}
		for i, m := range manifests {
			path := filepath.Join(*outDir, fmt.Sprintf("manifest-%02d.yaml", i+1))
			if err := os.WriteFile(path, []byte(m), 0o644); err != nil {
				fatal("write manifest", err)
			}
		}
	}

	if !*apply {
		for i, m := range manifests {
			if i > 0 {
				fmt.Println("---")
			}
			fmt.Print(m)
		}
		return
	}

	var taskRunName string
	for _, m := range manifests {
		if isTaskRun(m) {
			name, err := kubectlCreateName(m, in.Namespace)
			if err != nil {
				fatal("kubectl create", err)
			}
			taskRunName = name
		} else {
			if err := kubectlApply(m); err != nil {
				fatal("kubectl apply", err)
			}
		}
	}

	if in.Source.Type == "zip" && in.AppName != "" && taskRunName != "" {
		if err := handleZipDeploy(in, taskRunName); err != nil {
			fatal("zip deploy", err)
		}
	}
}

func runServer(addr, apiKey string) {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	http.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if apiKey != "" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+apiKey {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body failed", http.StatusBadRequest)
			return
		}

		var in Input
		if err := json.Unmarshal(body, &in); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		manifests, err := buildManifests(&in)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		dryRun := r.URL.Query().Get("dry_run") == "true"
		if dryRun {
			w.Header().Set("Content-Type", "application/yaml")
			for i, m := range manifests {
				if i > 0 {
					w.Write([]byte("\n---\n"))
				}
				w.Write([]byte(m))
			}
			return
		}

		var taskRunName string
		for _, m := range manifests {
			if isTaskRun(m) {
				name, err := kubectlCreateName(m, in.Namespace)
				if err != nil {
					http.Error(w, "kubectl create failed", http.StatusInternalServerError)
					return
				}
				taskRunName = name
			} else {
				if err := kubectlApply(m); err != nil {
					http.Error(w, "kubectl apply failed", http.StatusInternalServerError)
					return
				}
			}
		}

		if in.Source.Type == "zip" && in.AppName != "" && taskRunName != "" {
			go func(req Input, tr string) {
				if err := handleZipDeploy(req, tr); err != nil {
					log.Printf("zip deploy error: %v", err)
				}
			}(in, taskRunName)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"submitted"}`))
	})

	http.HandleFunc("/endpoint", func(w http.ResponseWriter, r *http.Request) {
		workspace := r.URL.Query().Get("workspace")
		app := r.URL.Query().Get("app")
		if workspace == "" || app == "" {
			http.Error(w, "workspace and app are required", http.StatusBadRequest)
			return
		}
		key := workspace + "/" + app
		serverState.mu.Lock()
		if url, ok := serverState.endpoints[key]; ok {
			serverState.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(fmt.Sprintf(`{"endpoint":"%s"}`, url)))
			return
		}
		serverState.mu.Unlock()

		kcfgPath := filepath.Join("/home/beko/kubeconfigs", workspace+".yaml")
		port, err := getServiceNodePort(kcfgPath, workspace, app)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		host := serverHostIP
		if host == "" {
			host = "127.0.0.1"
		}
		url := fmt.Sprintf("http://%s:%d", host, port)
		serverState.mu.Lock()
		serverState.endpoints[key] = url
		serverState.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"endpoint":"%s"}`, url)))
	})

	http.HandleFunc("/workspaces", func(w http.ResponseWriter, r *http.Request) {
		list, err := listWorkspaces()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(list)
	})

	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func buildManifests(in *Input) ([]string, error) {
	setDefaults(in)
	if err := validate(in); err != nil {
		return nil, err
	}

	manifests := make([]string, 0, 4)
	if in.Source.Type == "git" && in.Source.GitUsername != "" && in.Source.GitToken != "" {
		if in.Source.GitSecret == "" {
			in.Source.GitSecret = "git-cred-" + randSuffix()
		}
		manifests = append(manifests, renderGitSecret(*in))
	}

	if in.Source.Type == "local" {
		if in.Source.NFS != nil {
			pv, pvc := renderNFS(*in)
			manifests = append(manifests, pv, pvc)
		} else if in.Source.SMB != nil {
			secret, pv, pvc := renderSMB(*in)
			manifests = append(manifests, secret, pv, pvc)
		}
	}

	manifests = append(manifests, renderTaskRun(*in))
	return manifests, nil
}

func isTaskRun(m string) bool {
	return strings.Contains(m, "\nkind: TaskRun\n") || strings.HasPrefix(m, "kind: TaskRun\n")
}

func kubectlApply(m string) error {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(m)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func kubectlCreateName(m, ns string) (string, error) {
	cmd := exec.Command("kubectl", "-n", ns, "create", "-f", "-", "-o", "name")
	cmd.Stdin = strings.NewReader(m)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	parts := strings.Split(strings.TrimSpace(out.String()), "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("unexpected create output: %s", out.String())
	}
	return parts[1], nil
}

func handleZipDeploy(in Input, taskRunName string) error {
	ns := in.Namespace
	if err := waitForTaskRun(ns, taskRunName, 45*time.Minute); err != nil {
		return err
	}

	clusterName := "ws-" + sanitizeName(in.AppName)
	kcfgDir := "/home/beko/kubeconfigs"
	if err := os.MkdirAll(kcfgDir, 0o755); err != nil {
		return err
	}
	kcfgPath := filepath.Join(kcfgDir, clusterName+".yaml")

	if err := ensureKindCluster(clusterName, kcfgPath); err != nil {
		return err
	}

	image := fmt.Sprintf("%s/%s/%s:%s", in.Image.Registry, strings.ToLower(in.Image.Project), strings.ToLower(in.Image.Project), in.Image.Tag)
	if err := applyDeployment(kcfgPath, clusterName, sanitizeName(in.AppName), image, in.Deploy.ContainerPort); err != nil {
		return err
	}

	port, err := getServiceNodePort(kcfgPath, clusterName, sanitizeName(in.AppName))
	if err == nil {
		host := serverHostIP
		if host == "" {
			host = "127.0.0.1"
		}
		url := fmt.Sprintf("http://%s:%d", host, port)
		key := clusterName + "/" + sanitizeName(in.AppName)
		serverState.mu.Lock()
		serverState.endpoints[key] = url
		serverState.mu.Unlock()
	}
	return nil
}

func waitForTaskRun(ns, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := exec.Command("kubectl", "-n", ns, "get", "taskrun", name, "-o", "jsonpath={.status.conditions[0].status}")
		out, _ := cmd.CombinedOutput()
		status := strings.TrimSpace(string(out))
		if status == "True" {
			return nil
		}
		if status == "False" {
			cmd = exec.Command("kubectl", "-n", ns, "get", "taskrun", name, "-o", "jsonpath={.status.conditions[0].message}")
			msg, _ := cmd.CombinedOutput()
			return fmt.Errorf("taskrun failed: %s", strings.TrimSpace(string(msg)))
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("taskrun timeout: %s", name)
}

func ensureKindCluster(name, kubeconfigPath string) error {
	cmd := exec.Command("kind", "get", "clusters")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kind get clusters: %v", err)
	}
	if !strings.Contains(string(out), name) {
		cmd = exec.Command("kind", "create", "cluster", "--name", name)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("kind create cluster: %v", err)
		}
	}

	cmd = exec.Command("kind", "get", "kubeconfig", "--name", name)
	out, err = cmd.Output()
	if err != nil {
		return fmt.Errorf("kind get kubeconfig: %v", err)
	}
	return os.WriteFile(kubeconfigPath, out, 0o600)
}

func applyDeployment(kubeconfig, namespace, app, image string, port int) error {
	if port == 0 {
		port = 8080
	}
	manifest := renderDeployment(namespace, app, image, port)
	cmd := exec.Command("kubectl", "--kubeconfig", kubeconfig, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func renderDeployment(ns, app, image string, port int) string {
	tpl := `apiVersion: v1
kind: Namespace
metadata:
  name: {{.Namespace}}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{.App}}
  namespace: {{.Namespace}}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: {{.App}}
  template:
    metadata:
      labels:
        app: {{.App}}
    spec:
      containers:
        - name: {{.App}}
          image: {{.Image}}
          ports:
            - containerPort: {{.Port}}
---
apiVersion: v1
kind: Service
metadata:
  name: {{.App}}
  namespace: {{.Namespace}}
spec:
  type: NodePort
  selector:
    app: {{.App}}
  ports:
    - port: 80
      targetPort: {{.Port}}
`
	return mustRender(tpl, map[string]string{
		"Namespace": ns,
		"App":       app,
		"Image":     image,
		"Port":      fmt.Sprintf("%d", port),
	})
}

func sanitizeName(in string) string {
	s := strings.ToLower(in)
	re := regexp.MustCompile(`[^a-z0-9-]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "app"
	}
	return s
}

func getServiceNodePort(kubeconfig, namespace, app string) (int, error) {
	cmd := exec.Command("kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "svc", app, "-o", "jsonpath={.spec.ports[0].nodePort}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("get service nodePort failed: %v", err)
	}
	portStr := strings.TrimSpace(string(out))
	if portStr == "" {
		return 0, fmt.Errorf("nodePort not found")
	}
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		return 0, fmt.Errorf("invalid nodePort: %s", portStr)
	}
	return port, nil
}

func listWorkspaces() ([]byte, error) {
	cmd := exec.Command("kind", "get", "clusters")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kind get clusters: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var workspaces []map[string]any
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" || !strings.HasPrefix(name, "ws-") {
			continue
		}
		kcfg := filepath.Join("/home/beko/kubeconfigs", name+".yaml")
		apps, _ := listWorkspaceApps(kcfg, name)
		workspaces = append(workspaces, map[string]any{
			"workspace": name,
			"apps":      apps,
		})
	}
	return json.Marshal(workspaces)
}

func listWorkspaceApps(kubeconfig, namespace string) ([]map[string]any, error) {
	cmd := exec.Command("kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "svc", "-o", `jsonpath={range .items[*]}{.metadata.name}|{.spec.ports[0].nodePort}{"\n"}{end}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var apps []map[string]any
	for _, l := range lines {
		parts := strings.Split(l, "|")
		if len(parts) != 2 {
			continue
		}
		name := parts[0]
		portStr := parts[1]
		var port int
		fmt.Sscanf(portStr, "%d", &port)
		apps = append(apps, map[string]any{
			"app":      name,
			"nodePort": port,
		})
	}
	return apps, nil
}

func setDefaults(in *Input) {
	if in.Namespace == "" {
		in.Namespace = "tekton-pipelines"
	}
	if in.Task == "" {
		in.Task = "build-and-push-generic"
	}
	if in.Source.Revision == "" {
		in.Source.Revision = "main"
	}
	if in.Image.Registry == "" {
		in.Image.Registry = "lenovo:8443"
	}
	if in.Image.Tag == "" {
		in.Image.Tag = "latest"
	}
	if in.Source.NFS != nil && in.Source.NFS.Size == "" {
		in.Source.NFS.Size = "50Gi"
	}
	if in.Source.SMB != nil && in.Source.SMB.Size == "" {
		in.Source.SMB.Size = "50Gi"
	}
	if in.Deploy.ContainerPort == 0 {
		in.Deploy.ContainerPort = 8080
	}
}

func validate(in *Input) error {
	if in.Source.Type != "git" && in.Source.Type != "local" && in.Source.Type != "zip" {
		return fmt.Errorf("source.type must be git, local, or zip")
	}
	if in.Image.Project == "" {
		return fmt.Errorf("image.project is required")
	}
	if in.Source.Type == "git" {
		if in.Source.RepoURL == "" {
			return fmt.Errorf("source.repo_url is required for git")
		}
	}
	if in.Source.Type == "local" {
		if in.Source.LocalPath == "" {
			return fmt.Errorf("source.local_path is required for local")
		}
		if in.Source.PVCName == "" && in.Source.NFS == nil && in.Source.SMB == nil {
			return fmt.Errorf("source.pvc_name or source.nfs/source.smb is required for local")
		}
	}
	if in.Source.Type == "zip" {
		if in.Source.ZipURL == "" {
			return fmt.Errorf("source.zip_url is required for zip")
		}
		if in.AppName == "" {
			return fmt.Errorf("app_name is required for zip deployments")
		}
	}
	return nil
}

func renderGitSecret(in Input) string {
	tpl := `apiVersion: v1
kind: Secret
metadata:
  name: {{.GitSecret}}
  namespace: {{.Namespace}}
type: Opaque
stringData:
  username: {{.GitUsername}}
  token: {{.GitToken}}
`
	return mustRender(tpl, map[string]string{
		"GitSecret":   in.Source.GitSecret,
		"Namespace":   in.Namespace,
		"GitUsername": in.Source.GitUsername,
		"GitToken":    in.Source.GitToken,
	})
}

func renderNFS(in Input) (string, string) {
	id := randSuffix()
	pvName := "pv-nfs-" + id
	pvcName := in.Source.PVCName
	if pvcName == "" {
		pvcName = "pvc-nfs-" + id
		in.Source.PVCName = pvcName
	}

	pvTpl := `apiVersion: v1
kind: PersistentVolume
metadata:
  name: {{.PVName}}
spec:
  capacity:
    storage: {{.Size}}
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  nfs:
    server: {{.Server}}
    path: {{.Path}}
`
	pvcTpl := `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{.PVCName}}
  namespace: {{.Namespace}}
spec:
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: {{.Size}}
  volumeName: {{.PVName}}
`

	pv := mustRender(pvTpl, map[string]string{
		"PVName": pvName,
		"Size":   in.Source.NFS.Size,
		"Server": in.Source.NFS.Server,
		"Path":   in.Source.NFS.Path,
	})
	pvc := mustRender(pvcTpl, map[string]string{
		"PVCName":  pvcName,
		"Namespace": in.Namespace,
		"Size":     in.Source.NFS.Size,
		"PVName":   pvName,
	})

	return pv, pvc
}

func renderSMB(in Input) (string, string, string) {
	id := randSuffix()
	pvName := "pv-smb-" + id
	pvcName := in.Source.PVCName
	if pvcName == "" {
		pvcName = "pvc-smb-" + id
		in.Source.PVCName = pvcName
	}
	secretName := in.Source.SMB.SecretName
	if secretName == "" {
		secretName = "smb-cred-" + id
		in.Source.SMB.SecretName = secretName
	}
	volHandle := in.Source.SMB.VolumeHandle
	if volHandle == "" {
		volHandle = "smb-" + id
		in.Source.SMB.VolumeHandle = volHandle
	}

	secretTpl := `apiVersion: v1
kind: Secret
metadata:
  name: {{.SecretName}}
  namespace: {{.Namespace}}
type: Opaque
stringData:
  username: {{.Username}}
  password: {{.Password}}
`

	pvTpl := `apiVersion: v1
kind: PersistentVolume
metadata:
  name: {{.PVName}}
spec:
  capacity:
    storage: {{.Size}}
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  csi:
    driver: smb.csi.k8s.io
    volumeHandle: {{.VolumeHandle}}
    volumeAttributes:
      source: "//{{.Server}}/{{.Share}}"
    nodeStageSecretRef:
      name: {{.SecretName}}
      namespace: {{.Namespace}}
`

	pvcTpl := `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{.PVCName}}
  namespace: {{.Namespace}}
spec:
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: {{.Size}}
  volumeName: {{.PVName}}
`

	secret := mustRender(secretTpl, map[string]string{
		"SecretName": in.Source.SMB.SecretName,
		"Namespace":  in.Namespace,
		"Username":   in.Source.SMB.Username,
		"Password":   in.Source.SMB.Password,
	})

	pv := mustRender(pvTpl, map[string]string{
		"PVName":       pvName,
		"Size":         in.Source.SMB.Size,
		"VolumeHandle": in.Source.SMB.VolumeHandle,
		"Server":       in.Source.SMB.Server,
		"Share":        in.Source.SMB.Share,
		"SecretName":   in.Source.SMB.SecretName,
		"Namespace":    in.Namespace,
	})

	pvc := mustRender(pvcTpl, map[string]string{
		"PVCName":  pvcName,
		"Namespace": in.Namespace,
		"Size":     in.Source.SMB.Size,
		"PVName":   pvName,
	})

	return secret, pv, pvc
}

func renderTaskRun(in Input) string {
	ctx := RenderContext{
		Namespace:  in.Namespace,
		Task:       in.Task,
		SourceType: in.Source.Type,
		RepoURL:    in.Source.RepoURL,
		Revision:   in.Source.Revision,
		LocalPath:  in.Source.LocalPath,
		Project:    in.Image.Project,
		Tag:        in.Image.Tag,
		Registry:   in.Image.Registry,
		ZipURL:     in.Source.ZipURL,
		ZipUsername: in.Source.ZipUsername,
		ZipPassword: in.Source.ZipPassword,
		GitSecret:  in.Source.GitSecret,
		PVCName:    in.Source.PVCName,
		HasGit:     in.Source.Type == "git" && in.Source.GitUsername != "" && in.Source.GitToken != "",
		HasLocal:   in.Source.Type == "local",
		HasZip:     in.Source.Type == "zip",
	}

	tpl := `apiVersion: tekton.dev/v1
kind: TaskRun
metadata:
  generateName: build-and-push-run-
  namespace: {{.Namespace}}
spec:
  serviceAccountName: build-bot
  taskRef:
    name: {{.Task}}
  params:
    - name: source-type
      value: {{.SourceType}}
{{- if eq .SourceType "git" }}
    - name: repo-url
      value: {{.RepoURL}}
    - name: revision
      value: {{.Revision}}
{{- end }}
{{- if eq .SourceType "zip" }}
    - name: zip-url
      value: {{.ZipURL}}
    - name: zip-username
      value: {{.ZipUsername}}
    - name: zip-password
      value: {{.ZipPassword}}
{{- end }}
    - name: project
      value: {{.Project}}
    - name: registry
      value: {{.Registry}}
    - name: tag
      value: {{.Tag}}
{{- if eq .SourceType "local" }}
    - name: local-path
      value: {{.LocalPath}}
{{- end }}
  workspaces:
    - name: source
      emptyDir: {}
{{- if .HasGit }}
    - name: git-credentials
      secret:
        secretName: {{.GitSecret}}
{{- end }}
{{- if .HasLocal }}
    - name: local-source
      persistentVolumeClaim:
        claimName: {{.PVCName}}
{{- end }}
`

	return mustRender(tpl, ctx)
}

func mustRender(tpl string, data any) string {
	t, err := template.New("tpl").Parse(tpl)
	if err != nil {
		fatal("parse template", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		fatal("render template", err)
	}
	return buf.String()
}

func randSuffix() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().Unix())
	}
	return hex.EncodeToString(b)
}

func fatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", msg, err)
	os.Exit(1)
}

func fatalMsg(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
