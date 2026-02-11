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
	"strings"
	"text/template"
	"time"
)

type Input struct {
	Namespace string `json:"namespace"`
	Task      string `json:"task"`
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

func main() {
	inPath := flag.String("in", "", "input JSON file (default: stdin)")
	outDir := flag.String("out-dir", "", "output directory (default: stdout only)")
	apply := flag.Bool("apply", false, "kubectl apply generated manifests")
	server := flag.Bool("server", false, "run HTTP server")
	addr := flag.String("addr", ":8088", "server listen address")
	apiKey := flag.String("api-key", "", "optional API key for server auth (Bearer)")
	flag.Parse()

	if *server {
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

		for _, m := range manifests {
			cmd := exec.Command("kubectl", "create", "-f", "-")
			cmd.Stdin = strings.NewReader(m)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fatal("kubectl apply", err)
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

		for _, m := range manifests {
			cmd := exec.Command("kubectl", "create", "-f", "-")
			cmd.Stdin = strings.NewReader(m)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				http.Error(w, "kubectl create failed", http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"submitted"}`))
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
