package instctrl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/repo"
)

// EnsureHelmRepo checks if the cilium Helm repo exists in the local Helm configuration.
// If the repositories.yaml file does not exist, it creates an empty one.
// If the "cilium" repo is missing, it adds and updates it automatically.
func EnsureHelmRepo() error {
	settings := cli.New()
	repoFile := settings.RepositoryConfig

	// If the Helm repositories file does not exist, create the config directory and an empty file
	if _, err := os.Stat(repoFile); os.IsNotExist(err) {
		// Create the parent directory if missing
		if err := os.MkdirAll(filepath.Dir(repoFile), 0755); err != nil {
			return fmt.Errorf("failed to create helm repo config directory: %v", err)
		}
		// Write an empty repo file (with a valid Helm structure)
		emptyFile := repo.NewFile()
		if err := emptyFile.WriteFile(repoFile, 0644); err != nil {
			return fmt.Errorf("failed to write empty helm repo file: %v", err)
		}
	}

	// Load the Helm repositories configuration file
	rf, err := repo.LoadFile(repoFile)
	if err != nil {
		return fmt.Errorf("failed to load helm repo file: %v", err)
	}

	// Check if the cilium repo exists, and add it if missing
	if !rf.Has("cilium") {
		entry := &repo.Entry{
			Name: "cilium",
			URL:  "https://helm.cilium.io/",
		}
		rf.Update(entry)
		if err := rf.WriteFile(repoFile, 0644); err != nil {
			return fmt.Errorf("failed to write helm repo file: %v", err)
		}
		fmt.Println("Cilium helm repo added.")
		// Update Helm repo index to fetch latest charts
		cmd := exec.Command("helm", "repo", "update")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to update helm repo: %v", err)
		}
	} else {
		fmt.Println("Cilium helm repo already exists.")
	}
	return nil
}

// EnsureCiliumCLI checks if cilium CLI exists, and installs it if missing.
func EnsureCiliumCLI() error {
	_, err := exec.LookPath("cilium")
	if err == nil {
		fmt.Println("Cilium CLI is already installed.")
		return nil
	}
	// Detect OS and arch
	osName := runtime.GOOS
	arch := runtime.GOARCH
	// Compose URL
	url := fmt.Sprintf("https://github.com/cilium/cilium-cli/releases/latest/download/cilium-%s-%s.tar.gz", osName, arch)
	fmt.Printf("Downloading Cilium CLI from: %s\n", url)
	tmpDir := os.TempDir()
	archive := filepath.Join(tmpDir, "cilium-cli.tgz")
	out, err := os.Create(archive)
	if err != nil {
		return fmt.Errorf("cannot create temp file: %v", err)
	}
	defer out.Close()

	// Download file
	cmd := exec.Command("curl", "-L", "-o", archive, url)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to download cilium CLI: %v", err)
	}

	// Extract tar.gz
	cmd = exec.Command("tar", "xzf", archive, "-C", tmpDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract cilium CLI: %v", err)
	}
	// Move binary to /usr/local/bin
	binPath := filepath.Join(tmpDir, "cilium")
	destPath := "/usr/local/bin/cilium"
	cmd = exec.Command("sudo", "mv", binPath, destPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to move cilium CLI: %v", err)
	}
	// Make executable
	cmd = exec.Command("sudo", "chmod", "+x", destPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to chmod cilium CLI: %v", err)
	}

	fmt.Println("Cilium CLI installed at /usr/local/bin/cilium")
	return nil
}

// InstallCiliumIfAbsent ensures Cilium is installed in the host cluster via Helm.
// If already installed, it does nothing.
func InstallCiliumIfAbsent() error {
	// 1. Ensure helm repo exists
	if err := EnsureHelmRepo(); err != nil {
		return err
	}
	// 2. Optionally ensure cilium CLI exists
	if err := EnsureCiliumCLI(); err != nil {
		return err
	}
	// 3. Standard Helm install
	settings := cli.New()
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(settings.RESTClientGetter(), "kube-system", os.Getenv("HELM_DRIVER"), func(format string, v ...interface{}) { fmt.Printf(format, v...) }); err != nil {
		return fmt.Errorf("Helm init failed: %v", err)
	}

	chartPath, err := action.NewInstall(actionConfig).LocateChart("cilium/cilium", settings)
	if err != nil {
		return fmt.Errorf("Failed to locate Cilium chart: %v", err)
	}
	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("Failed to load Cilium chart: %v", err)
	}

	vals := map[string]interface{}{
		"ipam":     map[string]interface{}{"mode": "kubernetes"},
		"operator": map[string]interface{}{"replicas": 1},
		// Add more chart values if necessary
	}

	install := action.NewInstall(actionConfig)
	install.ReleaseName = "cilium"
	install.Namespace = "kube-system"
	install.CreateNamespace = true

	// Check if Cilium is already installed
	if _, err := action.NewHistory(actionConfig).Run("cilium"); err == nil {
		fmt.Println("Cilium is already installed in the cluster.")
		return nil
	}

	_, err = install.Run(chart, vals)
	if err != nil {
		return fmt.Errorf("Cilium install failed: %v", err)
	}
	fmt.Println("Cilium installation successful!")
	return nil
}
