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
	Workspace string `json:"workspace"`
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

type ExternalPortEntry struct {
	Workspace    string `json:"workspace"`
	App          string `json:"app"`
	ExternalPort int    `json:"external_port"`
}

type ExternalPortStore struct {
	mu      sync.Mutex
	path    string
	entries []ExternalPortEntry
}

var serverHostIP string
var serverKubeconfig string
var serverState = &ServerState{endpoints: map[string]string{}}
var portStore = &ExternalPortStore{path: "/home/beko/port-map.json"}
type Forward struct {
	Port int
	Cmd  *exec.Cmd
}

var forwardMu sync.Mutex
var forwards = map[string]*Forward{}

func main() {
	inPath := flag.String("in", "", "input JSON file (default: stdin)")
	outDir := flag.String("out-dir", "", "output directory (default: stdout only)")
	apply := flag.Bool("apply", false, "kubectl apply generated manifests")
	server := flag.Bool("server", false, "run HTTP server")
	addr := flag.String("addr", ":8088", "server listen address")
	apiKey := flag.String("api-key", "", "optional API key for server auth (Bearer)")
	hostIP := flag.String("host-ip", "", "host IP for endpoint generation (optional)")
	kubeconfig := flag.String("kubeconfig", "", "kubeconfig for kubectl (optional)")
	flag.Parse()

	if *server {
		serverHostIP = *hostIP
		serverKubeconfig = *kubeconfig
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
	if err := portStore.load(); err != nil {
		log.Printf("port map load error: %v", err)
	}
	// Start port forwards for existing mappings
	for _, e := range portStore.list() {
		if err := ensureForward(e.Workspace, e.App, e.ExternalPort); err != nil {
			log.Printf("forward start failed for %s/%s: %v", e.Workspace, e.App, err)
		}
	}

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

		if in.AppName != "" && taskRunName != "" && (in.Source.Type == "zip" || in.Source.Type == "git" || in.Source.Type == "local") {
			go func(req Input, tr string) {
				if err := handleZipDeploy(req, tr); err != nil {
					log.Printf("deploy error: %v", err)
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

	http.HandleFunc("/workspace/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		workspace := r.URL.Query().Get("workspace")
		if workspace == "" {
			http.Error(w, "workspace is required", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(workspace, "ws-") {
			http.Error(w, "workspace must start with ws-", http.StatusBadRequest)
			return
		}
		if err := deleteWorkspace(workspace); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"deleted"}`))
	})

	http.HandleFunc("/workspace/status", func(w http.ResponseWriter, r *http.Request) {
		workspace := r.URL.Query().Get("workspace")
		if workspace == "" {
			http.Error(w, "workspace is required", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(workspace, "ws-") {
			http.Error(w, "workspace must start with ws-", http.StatusBadRequest)
			return
		}
		info, err := getWorkspaceStatus(workspace)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(info)
	})

	http.HandleFunc("/app/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		workspace := r.URL.Query().Get("workspace")
		app := r.URL.Query().Get("app")
		if workspace == "" || app == "" {
			http.Error(w, "workspace and app are required", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(workspace, "ws-") {
			http.Error(w, "workspace must start with ws-", http.StatusBadRequest)
			return
		}
		if err := deleteApp(workspace, app); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"deleted"}`))
	})

	http.HandleFunc("/app/status", func(w http.ResponseWriter, r *http.Request) {
		workspace := r.URL.Query().Get("workspace")
		app := r.URL.Query().Get("app")
		if workspace == "" || app == "" {
			http.Error(w, "workspace and app are required", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(workspace, "ws-") {
			http.Error(w, "workspace must start with ws-", http.StatusBadRequest)
			return
		}
		info, err := getAppStatus(workspace, app)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(info)
	})

	http.HandleFunc("/workspace/scale", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		workspace := r.URL.Query().Get("workspace")
		app := r.URL.Query().Get("app")
		replicas := r.URL.Query().Get("replicas")
		if workspace == "" || app == "" || replicas == "" {
			http.Error(w, "workspace, app, replicas are required", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(workspace, "ws-") {
			http.Error(w, "workspace must start with ws-", http.StatusBadRequest)
			return
		}
		if err := scaleApp(workspace, app, replicas); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"scaled"}`))
	})

	http.HandleFunc("/app/restart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		workspace := r.URL.Query().Get("workspace")
		app := r.URL.Query().Get("app")
		if workspace == "" || app == "" {
			http.Error(w, "workspace and app are required", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(workspace, "ws-") {
			http.Error(w, "workspace must start with ws-", http.StatusBadRequest)
			return
		}
		if err := rolloutRestart(workspace, app); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"restarted"}`))
	})

	http.HandleFunc("/workspace/restart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		workspace := r.URL.Query().Get("workspace")
		if workspace == "" {
			http.Error(w, "workspace is required", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(workspace, "ws-") {
			http.Error(w, "workspace must start with ws-", http.StatusBadRequest)
			return
		}
		if err := rolloutRestartAll(workspace); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"restarted"}`))
	})

	http.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(openAPISpec()))
	})

	http.HandleFunc("/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(swaggerHTML()))
	})

	http.HandleFunc("/hostinfo", func(w http.ResponseWriter, r *http.Request) {
		host := serverHostIP
		if host == "" {
			host = strings.Split(r.Host, ":")[0]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf(`{"host_ip":"%s"}`, host)))
	})

	http.HandleFunc("/external-map", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			entries := portStore.list()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			b, _ := json.Marshal(entries)
			w.Write(b)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req ExternalPortEntry
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if req.Workspace == "" || req.App == "" || req.ExternalPort <= 0 {
			http.Error(w, "workspace, app and external_port are required", http.StatusBadRequest)
			return
		}
		if err := portStore.upsert(req); err != nil {
			if err == errPortConflict {
				http.Error(w, "port already in use", http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := ensureForward(req.Workspace, req.App, req.ExternalPort); err != nil {
			_ = portStore.remove(req.Workspace, req.App)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Optional UI at /ui/ (served from ./ui next to the binary)
	if uiDir := findUIDir(); uiDir != "" {
		fs := http.FileServer(http.Dir(uiDir))
		http.Handle("/ui/", http.StripPrefix("/ui/", fs))
		http.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ui/", http.StatusFound)
		})
	}

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
	cmd := kubectlCmd("apply", "-f", "-")
	cmd.Stdin = strings.NewReader(m)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func kubectlCreateName(m, ns string) (string, error) {
	cmd := kubectlCmd("-n", ns, "create", "-f", "-", "-o", "name")
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

func kubectlCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("kubectl", args...)
	if serverKubeconfig != "" {
		cmd.Env = append(os.Environ(), "KUBECONFIG="+serverKubeconfig)
	}
	return cmd
}

func handleZipDeploy(in Input, taskRunName string) error {
	ns := in.Namespace
	if err := waitForTaskRun(ns, taskRunName, 45*time.Minute); err != nil {
		return err
	}

	clusterName := in.Workspace
	if clusterName == "" {
		clusterName = "ws-" + sanitizeName(in.AppName)
	}
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
		cmd := kubectlCmd("-n", ns, "get", "taskrun", name, "-o", "jsonpath={.status.conditions[0].status}")
		out, _ := cmd.CombinedOutput()
		status := strings.TrimSpace(string(out))
		if status == "True" {
			return nil
		}
		if status == "False" {
			cmd = kubectlCmd("-n", ns, "get", "taskrun", name, "-o", "jsonpath={.status.conditions[0].message}")
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
		image := kindNodeImage()
		cmd = exec.Command("kind", "create", "cluster", "--name", name, "--image", image)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("kind create cluster: %v", err)
		}
	}

	if err := configureKindNode(name); err != nil {
		return err
	}

	cmd = exec.Command("kind", "get", "kubeconfig", "--name", name)
	out, err = cmd.Output()
	if err != nil {
		return fmt.Errorf("kind get kubeconfig: %v", err)
	}
	return os.WriteFile(kubeconfigPath, out, 0o600)
}

func kindNodeImage() string {
	if v := strings.TrimSpace(os.Getenv("KIND_NODE_IMAGE")); v != "" {
		return v
	}
	return "kindest/node:v1.31.4"
}

func configureKindNode(clusterName string) error {
	node := clusterName + "-control-plane"
	// Ensure host mapping for Harbor
	cmd := exec.Command("docker", "exec", node, "sh", "-c", "grep -q ' lenovo' /etc/hosts || echo '172.18.0.1 lenovo' >> /etc/hosts")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("configure hosts: %v", err)
	}

	// Configure containerd to trust Harbor (skip TLS verify for test)
	hostsToml := "server = \"https://lenovo:8443\"\\n[host.\\\"https://lenovo:8443\\\"]\\n  capabilities = [\\\"pull\\\", \\\"resolve\\\"]\\n  skip_verify = true\\n"
	cmd = exec.Command("docker", "exec", node, "sh", "-c", "mkdir -p /etc/containerd/certs.d/lenovo:8443")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mkdir certs.d: %v", err)
	}
	cmd = exec.Command("docker", "exec", node, "sh", "-c", "printf '%s' \""+hostsToml+"\" > /etc/containerd/certs.d/lenovo:8443/hosts.toml")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("write hosts.toml: %v", err)
	}
	return nil
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

func deleteWorkspace(name string) error {
	cmd := exec.Command("kind", "delete", "cluster", "--name", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kind delete cluster: %v", err)
	}
	kcfg := filepath.Join("/home/beko/kubeconfigs", name+".yaml")
	_ = os.Remove(kcfg)

	serverState.mu.Lock()
	for k := range serverState.endpoints {
		if strings.HasPrefix(k, name+"/") {
			delete(serverState.endpoints, k)
		}
	}
	serverState.mu.Unlock()
	return nil
}

func deleteApp(workspace, app string) error {
	kcfg := filepath.Join("/home/beko/kubeconfigs", workspace+".yaml")
	cmd := exec.Command("kubectl", "--kubeconfig", kcfg, "-n", workspace, "delete", "deployment", app)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("delete deployment: %v", err)
	}
	cmd = exec.Command("kubectl", "--kubeconfig", kcfg, "-n", workspace, "delete", "service", app)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("delete service: %v", err)
	}

	serverState.mu.Lock()
	delete(serverState.endpoints, workspace+"/"+app)
	serverState.mu.Unlock()
	return nil
}

func getAppStatus(workspace, app string) ([]byte, error) {
	kcfg := filepath.Join("/home/beko/kubeconfigs", workspace+".yaml")
	podsCmd := exec.Command("kubectl", "--kubeconfig", kcfg, "-n", workspace, "get", "pods", "-l", "app="+app, "-o", "jsonpath={range .items[*]}{.metadata.name}|{.status.phase}{\"\\n\"}{end}")
	podsOut, podsErr := podsCmd.CombinedOutput()
	if podsErr != nil {
		return nil, fmt.Errorf("get app pods failed: %v", podsErr)
	}
	svcCmd := exec.Command("kubectl", "--kubeconfig", kcfg, "-n", workspace, "get", "svc", app, "-o", "jsonpath={.spec.ports[0].nodePort}")
	svcOut, svcErr := svcCmd.CombinedOutput()
	if svcErr != nil {
		return nil, fmt.Errorf("get app service failed: %v", svcErr)
	}

	var pods []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(podsOut)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) != 2 {
			continue
		}
		pods = append(pods, map[string]any{
			"name":  parts[0],
			"phase": parts[1],
		})
	}

	nodePort := strings.TrimSpace(string(svcOut))
	out := map[string]any{
		"workspace": workspace,
		"app":       app,
		"nodePort":  nodePort,
		"pods":      pods,
	}
	return json.Marshal(out)
}

func scaleApp(workspace, app, replicas string) error {
	kcfg := filepath.Join("/home/beko/kubeconfigs", workspace+".yaml")
	cmd := exec.Command("kubectl", "--kubeconfig", kcfg, "-n", workspace, "scale", "deployment", app, "--replicas", replicas)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("scale deployment: %v", err)
	}
	return nil
}

func rolloutRestart(workspace, app string) error {
	kcfg := filepath.Join("/home/beko/kubeconfigs", workspace+".yaml")
	cmd := exec.Command("kubectl", "--kubeconfig", kcfg, "-n", workspace, "rollout", "restart", "deployment", app)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rollout restart: %v", err)
	}
	return nil
}

func rolloutRestartAll(workspace string) error {
	kcfg := filepath.Join("/home/beko/kubeconfigs", workspace+".yaml")
	cmd := exec.Command("kubectl", "--kubeconfig", kcfg, "-n", workspace, "rollout", "restart", "deployment", "-l", "app")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rollout restart all: %v", err)
	}
	return nil
}

func openAPISpec() string {
	return `{
  "openapi": "3.0.3",
  "info": {
    "title": "Tekton Runner API",
    "version": "1.0.0"
  },
  "paths": {
    "/healthz": {
      "get": {
        "summary": "Health check",
        "responses": { "200": { "description": "OK" } }
      }
    },
    "/run": {
      "post": {
        "summary": "Create TaskRun",
        "requestBody": {
          "required": true,
          "content": { "application/json": { "schema": { "$ref": "#/components/schemas/RunRequest" } } }
        },
        "responses": { "202": { "description": "Submitted" } }
      }
    },
    "/endpoint": {
      "get": {
        "summary": "Get app endpoint",
        "parameters": [
          { "name": "workspace", "in": "query", "required": true, "schema": { "type": "string" } },
          { "name": "app", "in": "query", "required": true, "schema": { "type": "string" } }
        ],
        "responses": { "200": { "description": "Endpoint" } }
      }
    },
    "/hostinfo": {
      "get": {
        "summary": "Get host IP",
        "responses": { "200": { "description": "Host info" } }
      }
    },
    "/external-map": {
      "get": {
        "summary": "List external port mappings",
        "responses": { "200": { "description": "Mappings" } }
      },
      "post": {
        "summary": "Set external port mapping",
        "requestBody": {
          "required": true,
          "content": { "application/json": { "schema": { "$ref": "#/components/schemas/ExternalPortEntry" } } }
        },
        "responses": { "200": { "description": "Saved" }, "409": { "description": "Port conflict" } }
      }
    },
    "/workspaces": {
      "get": {
        "summary": "List workspaces",
        "responses": { "200": { "description": "Workspaces" } }
      }
    },
    "/workspace/status": {
      "get": {
        "summary": "Workspace status",
        "parameters": [
          { "name": "workspace", "in": "query", "required": true, "schema": { "type": "string" } }
        ],
        "responses": { "200": { "description": "Status" } }
      }
    },
    "/workspace/delete": {
      "post": {
        "summary": "Delete workspace",
        "parameters": [
          { "name": "workspace", "in": "query", "required": true, "schema": { "type": "string" } }
        ],
        "responses": { "200": { "description": "Deleted" } }
      }
    },
    "/workspace/scale": {
      "post": {
        "summary": "Scale app",
        "parameters": [
          { "name": "workspace", "in": "query", "required": true, "schema": { "type": "string" } },
          { "name": "app", "in": "query", "required": true, "schema": { "type": "string" } },
          { "name": "replicas", "in": "query", "required": true, "schema": { "type": "integer" } }
        ],
        "responses": { "200": { "description": "Scaled" } }
      }
    },
    "/workspace/restart": {
      "post": {
        "summary": "Restart all apps in workspace",
        "parameters": [
          { "name": "workspace", "in": "query", "required": true, "schema": { "type": "string" } }
        ],
        "responses": { "200": { "description": "Restarted" } }
      }
    },
    "/app/status": {
      "get": {
        "summary": "App status",
        "parameters": [
          { "name": "workspace", "in": "query", "required": true, "schema": { "type": "string" } },
          { "name": "app", "in": "query", "required": true, "schema": { "type": "string" } }
        ],
        "responses": { "200": { "description": "Status" } }
      }
    },
    "/app/delete": {
      "post": {
        "summary": "Delete app",
        "parameters": [
          { "name": "workspace", "in": "query", "required": true, "schema": { "type": "string" } },
          { "name": "app", "in": "query", "required": true, "schema": { "type": "string" } }
        ],
        "responses": { "200": { "description": "Deleted" } }
      }
    },
    "/app/restart": {
      "post": {
        "summary": "Restart app",
        "parameters": [
          { "name": "workspace", "in": "query", "required": true, "schema": { "type": "string" } },
          { "name": "app", "in": "query", "required": true, "schema": { "type": "string" } }
        ],
        "responses": { "200": { "description": "Restarted" } }
      }
    }
  },
  "components": {
    "schemas": {
      "RunRequest": {
        "type": "object",
        "properties": {
          "app_name": { "type": "string" },
          "workspace": { "type": "string" },
          "source": {
            "type": "object",
            "properties": {
              "type": { "type": "string", "enum": ["git","local","zip"] },
              "repo_url": { "type": "string" },
              "revision": { "type": "string" },
              "git_username": { "type": "string" },
              "git_token": { "type": "string" },
              "local_path": { "type": "string" },
              "pvc_name": { "type": "string" },
              "zip_url": { "type": "string" },
              "zip_username": { "type": "string" },
              "zip_password": { "type": "string" }
            }
          },
          "image": {
            "type": "object",
            "properties": {
              "project": { "type": "string" },
              "tag": { "type": "string" },
              "registry": { "type": "string" }
            }
          },
          "deploy": {
            "type": "object",
            "properties": {
              "container_port": { "type": "integer" }
            }
          }
        }
      },
      "ExternalPortEntry": {
        "type": "object",
        "properties": {
          "workspace": { "type": "string" },
          "app": { "type": "string" },
          "external_port": { "type": "integer" }
        }
      }
    }
  }
}`
}

func swaggerHTML() string {
	return `<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <title>Tekton Runner API Docs</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script>
      window.ui = SwaggerUIBundle({
        url: "/openapi.json",
        dom_id: "#swagger-ui"
      });
    </script>
  </body>
</html>`
}

var errPortConflict = fmt.Errorf("external port already in use")

func (s *ExternalPortStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.entries = []ExternalPortEntry{}
			return nil
		}
		return err
	}
	if len(data) == 0 {
		s.entries = []ExternalPortEntry{}
		return nil
	}
	var entries []ExternalPortEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	s.entries = entries
	return nil
}

func (s *ExternalPortStore) list() []ExternalPortEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ExternalPortEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

func (s *ExternalPortStore) upsert(in ExternalPortEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.ExternalPort == in.ExternalPort && (e.Workspace != in.Workspace || e.App != in.App) {
			return errPortConflict
		}
	}
	updated := false
	for i, e := range s.entries {
		if e.Workspace == in.Workspace && e.App == in.App {
			s.entries[i] = in
			updated = true
			break
		}
	}
	if !updated {
		s.entries = append(s.entries, in)
	}
	b, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *ExternalPortStore) remove(workspace, app string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ExternalPortEntry, 0, len(s.entries))
	for _, e := range s.entries {
		if e.Workspace == workspace && e.App == app {
			continue
		}
		out = append(out, e)
	}
	s.entries = out
	b, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func forwardKey(workspace, app string) string {
	return workspace + "::" + app
}

func ensureForward(workspace, app string, externalPort int) error {
	if externalPort <= 0 {
		return fmt.Errorf("invalid external port")
	}
	key := forwardKey(workspace, app)

	forwardMu.Lock()
	if fwd, ok := forwards[key]; ok && fwd != nil && fwd.Cmd != nil && fwd.Cmd.Process != nil {
		if fwd.Port == externalPort {
			forwardMu.Unlock()
			return nil
		}
		_ = fwd.Cmd.Process.Kill()
		delete(forwards, key)
	}
	forwardMu.Unlock()

	kcfg := filepath.Join("/home/beko/kubeconfigs", workspace+".yaml")
	nodePort, err := getServiceNodePort(kcfg, workspace, app)
	if err != nil {
		return err
	}
	nodeIP, err := getNodeInternalIP(kcfg, workspace)
	if err != nil {
		return err
	}

	if _, err := exec.LookPath("socat"); err != nil {
		return fmt.Errorf("socat not found")
	}

	args := []string{
		fmt.Sprintf("TCP-LISTEN:%d,bind=0.0.0.0,fork,reuseaddr", externalPort),
		fmt.Sprintf("TCP:%s:%d", nodeIP, nodePort),
	}
	cmd := exec.Command("socat", args...)
	logPath := fmt.Sprintf("/tmp/socat-%s-%s.log", workspace, app)
	f, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if f != nil {
		cmd.Stdout = f
		cmd.Stderr = f
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("socat start failed: %v", err)
	}
	forwardMu.Lock()
	forwards[key] = &Forward{Port: externalPort, Cmd: cmd}
	forwardMu.Unlock()
	return nil
}

func getNodeInternalIP(kubeconfig, workspace string) (string, error) {
	cmd := exec.Command("kubectl", "--kubeconfig", kubeconfig, "get", "node", "-o", "jsonpath={.items[0].status.addresses[?(@.type==\"InternalIP\")].address}")
	out, err := cmd.Output()
	if err == nil {
		ip := strings.TrimSpace(string(out))
		if ip != "" {
			return ip, nil
		}
	}
	// Fallback to docker inspect on kind node
	node := workspace + "-control-plane"
	inspect := exec.Command("docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", node)
	insOut, insErr := inspect.Output()
	if insErr != nil {
		return "", fmt.Errorf("node ip not found")
	}
	ip := strings.TrimSpace(string(insOut))
	if ip == "" {
		return "", fmt.Errorf("node ip empty")
	}
	return ip, nil
}

func findUIDir() string {
	exe, err := os.Executable()
	if err == nil {
		if dir := filepath.Dir(exe); dir != "" {
			if fileExists(filepath.Join(dir, "ui", "index.html")) {
				return filepath.Join(dir, "ui")
			}
		}
	}
	if fileExists(filepath.Join("ui", "index.html")) {
		return "ui"
	}
	return ""
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func getWorkspaceStatus(workspace string) ([]byte, error) {
	kcfg := filepath.Join("/home/beko/kubeconfigs", workspace+".yaml")
	podsCmd := exec.Command("kubectl", "--kubeconfig", kcfg, "-n", workspace, "get", "pods", "-o", "jsonpath={range .items[*]}{.metadata.name}|{.status.phase}{\"\\n\"}{end}")
	podsOut, podsErr := podsCmd.CombinedOutput()
	if podsErr != nil {
		return nil, fmt.Errorf("get pods failed: %v", podsErr)
	}

	servicesCmd := exec.Command("kubectl", "--kubeconfig", kcfg, "-n", workspace, "get", "svc", "-o", `jsonpath={range .items[*]}{.metadata.name}|{.spec.ports[0].nodePort}{"\n"}{end}`)
	svcOut, svcErr := servicesCmd.CombinedOutput()
	if svcErr != nil {
		return nil, fmt.Errorf("get services failed: %v", svcErr)
	}

	var pods []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(podsOut)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) != 2 {
			continue
		}
		pods = append(pods, map[string]any{
			"name":  parts[0],
			"phase": parts[1],
		})
	}

	var svcs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(svcOut)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) != 2 {
			continue
		}
		portStr := parts[1]
		var port int
		fmt.Sscanf(portStr, "%d", &port)
		svcs = append(svcs, map[string]any{
			"name":     parts[0],
			"nodePort": port,
		})
	}

	out := map[string]any{
		"workspace": workspace,
		"pods":      pods,
		"services":  svcs,
	}
	return json.Marshal(out)
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
	if in.Workspace != "" && !strings.HasPrefix(in.Workspace, "ws-") {
		return fmt.Errorf("workspace must start with ws-")
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
{{- if .ZipUsername }}
    - name: zip-username
      value: {{.ZipUsername}}
{{- end }}
{{- if .ZipPassword }}
    - name: zip-password
      value: {{.ZipPassword}}
{{- end }}
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
